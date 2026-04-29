#!/usr/bin/env python3
"""Migrate chatgpt2api file storage to the SQLite database backend."""

from __future__ import annotations

import argparse
import json
import os
import shutil
import sqlite3
import sys
import tempfile
from datetime import datetime, timezone
from pathlib import Path
from typing import Any


class MigrationError(Exception):
    pass


def parse_args() -> argparse.Namespace:
    default_data_dir = os.environ.get("DATA_DIR")
    if default_data_dir is None:
        default_data_dir = str(Path(__file__).resolve().parent.parent / "data")
    parser = argparse.ArgumentParser(
        description=(
            "Migrate data JSON files and logs.jsonl into the SQLite schema "
            "used by STORAGE_BACKEND=sqlite."
        )
    )
    parser.add_argument(
        "--data-dir",
        default=default_data_dir,
        help="directory containing chatgpt2api data files (default: repo-root/data)",
    )
    parser.add_argument(
        "--accounts-json",
        default=None,
        help="path to accounts.json (default: <data-dir>/accounts.json)",
    )
    parser.add_argument(
        "--auth-keys-json",
        default=None,
        help="path to auth_keys.json (default: <data-dir>/auth_keys.json)",
    )
    parser.add_argument(
        "--db",
        default=None,
        help="target SQLite database path (default: <data-dir>/chatgpt2api.db)",
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="validate and build a temporary database without replacing the target",
    )
    parser.add_argument(
        "--allow-empty",
        action="store_true",
        help="allow migration when no JSON or JSONL input files exist",
    )
    return parser.parse_args()


def read_json(path: Path, fallback: Any) -> Any:
    if not path.exists():
        return fallback
    if path.is_dir():
        raise MigrationError(f"{path} is a directory, expected a JSON file")
    try:
        with path.open("r", encoding="utf-8") as handle:
            text = handle.read().strip()
    except OSError as exc:
        raise MigrationError(f"failed to read {path}: {exc}") from exc
    if text == "":
        return fallback
    try:
        return json.loads(text)
    except json.JSONDecodeError as exc:
        raise MigrationError(f"invalid JSON in {path}: {exc}") from exc


def object_list(raw: Any, path: Path, label: str) -> list[dict[str, Any]]:
    if raw is None:
        return []
    if not isinstance(raw, list):
        raise MigrationError(f"{path} must contain a JSON array for {label}")
    out: list[dict[str, Any]] = []
    for index, item in enumerate(raw, start=1):
        if not isinstance(item, dict):
            raise MigrationError(f"{path} {label} item #{index} is not a JSON object")
        out.append(item)
    return out


def load_accounts(path: Path) -> list[dict[str, Any]]:
    return object_list(read_json(path, []), path, "accounts")


def load_auth_keys(path: Path) -> list[dict[str, Any]]:
    raw = read_json(path, [])
    if isinstance(raw, dict):
        raw = raw.get("items", [])
    return object_list(raw, path, "auth_keys")


def required_key(item: dict[str, Any], key: str, label: str, index: int) -> str:
    if key not in item or item[key] is None:
        raise MigrationError(f"{label} item #{index} is missing required field {key!r}")
    value = str(item[key]).strip()
    if value == "":
        raise MigrationError(f"{label} item #{index} has empty required field {key!r}")
    return value


def validate_unique(
    items: list[dict[str, Any]], source_key: str, label: str
) -> list[str]:
    seen: dict[str, int] = {}
    keys: list[str] = []
    for index, item in enumerate(items, start=1):
        value = required_key(item, source_key, label, index)
        if value in seen:
            raise MigrationError(
                f"duplicate {label} {source_key} {value!r} at items "
                f"#{seen[value]} and #{index}"
            )
        seen[value] = index
        keys.append(value)
    return keys


def encode_item(item: dict[str, Any]) -> str:
    return json.dumps(item, ensure_ascii=False, separators=(",", ":"))


def encode_value(value: Any) -> str:
    return json.dumps(value, ensure_ascii=False, separators=(",", ":"))


def collect_json_documents(data_dir: Path) -> list[tuple[str, Any]]:
    if not data_dir.exists():
        return []
    docs: list[tuple[str, Any]] = []
    for path in sorted(data_dir.rglob("*.json")):
        if not path.is_file():
            continue
        rel = path.relative_to(data_dir).as_posix()
        docs.append((rel, read_json(path, None)))
    return docs


def log_day(value: Any) -> str:
    text = str(value or "").strip()
    if len(text) < 10:
        return ""
    return text[:10]


def load_logs(path: Path) -> list[dict[str, Any]]:
    if not path.exists():
        return []
    if path.is_dir():
        raise MigrationError(f"{path} is a directory, expected a JSONL file")
    logs: list[dict[str, Any]] = []
    try:
        with path.open("r", encoding="utf-8") as handle:
            for line_no, line in enumerate(handle, start=1):
                line = line.strip()
                if line == "":
                    continue
                try:
                    item = json.loads(line)
                except json.JSONDecodeError as exc:
                    raise MigrationError(
                        f"invalid JSON in {path} line {line_no}: {exc}"
                    ) from exc
                if not isinstance(item, dict):
                    raise MigrationError(f"{path} line {line_no} is not a JSON object")
                logs.append(item)
    except OSError as exc:
        raise MigrationError(f"failed to read {path}: {exc}") from exc
    return logs


def configure_sqlite(conn: sqlite3.Connection) -> None:
    conn.execute("PRAGMA journal_mode=WAL")
    conn.execute("PRAGMA synchronous=NORMAL")
    conn.execute("PRAGMA busy_timeout=5000")
    conn.execute("PRAGMA temp_store=MEMORY")
    conn.execute("PRAGMA foreign_keys=ON")


def write_database(
    db_path: Path,
    accounts: list[dict[str, Any]],
    account_keys: list[str],
    auth_keys: list[dict[str, Any]],
    auth_key_ids: list[str],
    documents: list[tuple[str, Any]],
    logs: list[dict[str, Any]],
) -> dict[str, int]:
    conn = sqlite3.connect(str(db_path))
    try:
        configure_sqlite(conn)
        conn.execute(
            "CREATE TABLE IF NOT EXISTS accounts "
            "(id INTEGER PRIMARY KEY AUTOINCREMENT, "
            "access_token TEXT UNIQUE NOT NULL, data TEXT NOT NULL)"
        )
        conn.execute(
            "CREATE TABLE IF NOT EXISTS auth_keys "
            "(id INTEGER PRIMARY KEY AUTOINCREMENT, "
            "key_id TEXT UNIQUE NOT NULL, data TEXT NOT NULL)"
        )
        conn.execute(
            "CREATE TABLE IF NOT EXISTS json_documents "
            "(name TEXT PRIMARY KEY, data TEXT NOT NULL, updated_at TEXT NOT NULL)"
        )
        conn.execute(
            "CREATE TABLE IF NOT EXISTS logs "
            "(id INTEGER PRIMARY KEY AUTOINCREMENT, created_at TEXT NOT NULL, "
            "type TEXT NOT NULL, day TEXT NOT NULL, data TEXT NOT NULL)"
        )
        conn.execute(
            "CREATE INDEX IF NOT EXISTS idx_logs_type_day_id "
            "ON logs (type, day, id)"
        )
        conn.execute(
            "CREATE INDEX IF NOT EXISTS idx_logs_day_id ON logs (day, id)"
        )
        conn.execute("DELETE FROM accounts")
        conn.execute("DELETE FROM auth_keys")
        conn.execute("DELETE FROM json_documents")
        conn.execute("DELETE FROM logs")
        conn.executemany(
            "INSERT INTO accounts (access_token, data) VALUES (?, ?)",
            [(key, encode_item(item)) for key, item in zip(account_keys, accounts)],
        )
        conn.executemany(
            "INSERT INTO auth_keys (key_id, data) VALUES (?, ?)",
            [(key, encode_item(item)) for key, item in zip(auth_key_ids, auth_keys)],
        )
        now = datetime.now(timezone.utc).isoformat(timespec="microseconds")
        conn.executemany(
            "INSERT INTO json_documents (name, data, updated_at) VALUES (?, ?, ?)",
            [(name, encode_value(value), now) for name, value in documents],
        )
        conn.executemany(
            "INSERT INTO logs (created_at, type, day, data) VALUES (?, ?, ?, ?)",
            [
                (
                    str(item.get("time") or ""),
                    str(item.get("type") or "").strip(),
                    log_day(item.get("time")),
                    encode_value(item),
                )
                for item in logs
            ],
        )
        conn.commit()
        return {
            "accounts": int(conn.execute("SELECT COUNT(*) FROM accounts").fetchone()[0]),
            "auth_keys": int(conn.execute("SELECT COUNT(*) FROM auth_keys").fetchone()[0]),
            "documents": int(
                conn.execute("SELECT COUNT(*) FROM json_documents").fetchone()[0]
            ),
            "logs": int(conn.execute("SELECT COUNT(*) FROM logs").fetchone()[0]),
        }
    except Exception:
        conn.rollback()
        raise
    finally:
        conn.close()


def backup_path(target: Path) -> Path:
    stamp = datetime.now().strftime("%Y%m%d%H%M%S")
    candidate = target.with_name(f"{target.name}.bak-{stamp}")
    suffix = 1
    while candidate.exists():
        candidate = target.with_name(f"{target.name}.bak-{stamp}-{suffix}")
        suffix += 1
    return candidate


def build_temp_database(
    target: Path,
    accounts: list[dict[str, Any]],
    account_keys: list[str],
    auth_keys: list[dict[str, Any]],
    auth_key_ids: list[str],
    documents: list[tuple[str, Any]],
    logs: list[dict[str, Any]],
) -> tuple[Path, dict[str, int]]:
    target.parent.mkdir(parents=True, exist_ok=True)
    handle, temp_name = tempfile.mkstemp(
        prefix=f".{target.name}.",
        suffix=".tmp",
        dir=str(target.parent),
    )
    os.close(handle)
    temp_path = Path(temp_name)
    try:
        counts = write_database(
            temp_path,
            accounts,
            account_keys,
            auth_keys,
            auth_key_ids,
            documents,
            logs,
        )
    except Exception:
        temp_path.unlink(missing_ok=True)
        raise
    return temp_path, counts


def migrate(args: argparse.Namespace) -> None:
    data_dir = Path(args.data_dir).expanduser()
    accounts_path = (
        Path(args.accounts_json).expanduser()
        if args.accounts_json
        else data_dir / "accounts.json"
    )
    auth_keys_path = (
        Path(args.auth_keys_json).expanduser()
        if args.auth_keys_json
        else data_dir / "auth_keys.json"
    )
    db_path = Path(args.db).expanduser() if args.db else data_dir / "chatgpt2api.db"
    logs_path = data_dir / "logs.jsonl"
    documents = collect_json_documents(data_dir)
    logs = load_logs(logs_path)

    if (
        not args.allow_empty
        and not accounts_path.exists()
        and not auth_keys_path.exists()
        and not documents
        and not logs
    ):
        raise MigrationError(
            f"no JSON or JSONL input files found under {data_dir}"
        )

    accounts = load_accounts(accounts_path)
    auth_keys = load_auth_keys(auth_keys_path)
    account_keys = validate_unique(accounts, "access_token", "accounts")
    auth_key_ids = validate_unique(auth_keys, "id", "auth_keys")

    temp_path, counts = build_temp_database(
        db_path, accounts, account_keys, auth_keys, auth_key_ids, documents, logs
    )
    if args.dry_run:
        temp_path.unlink(missing_ok=True)
        print(
            "Dry run OK: "
            f"{counts['accounts']} accounts, {counts['auth_keys']} auth keys, "
            f"{counts['documents']} JSON documents, and {counts['logs']} logs validated."
        )
        return

    backup = None
    if db_path.exists():
        if db_path.is_dir():
            temp_path.unlink(missing_ok=True)
            raise MigrationError(f"{db_path} is a directory, expected a SQLite file")
        backup = backup_path(db_path)
        shutil.copy2(db_path, backup)
    os.replace(temp_path, db_path)
    os.chmod(db_path, 0o644)

    print(f"Migrated accounts: {counts['accounts']}")
    print(f"Migrated auth keys: {counts['auth_keys']}")
    print(f"Migrated JSON documents: {counts['documents']}")
    print(f"Migrated logs: {counts['logs']}")
    print(f"SQLite database: {db_path}")
    if backup is not None:
        print(f"Previous database backup: {backup}")


def main() -> int:
    try:
        migrate(parse_args())
    except MigrationError as exc:
        print(f"migration failed: {exc}", file=sys.stderr)
        return 1
    except sqlite3.Error as exc:
        print(f"migration failed: sqlite error: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
