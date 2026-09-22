#!/usr/bin/env python3
"""只读诊断脚本：分析 chatgpt2api 数据库的空间构成。

回答「这 200 多兆里都存了什么、有没有必要」：
  - 文件 / 页 / 空闲页的总体构成
  - json_documents 按名称前缀归类（生图元数据、缩略图元数据、任务表……）
  - 每个大类里最大的几个文档，以及它们的键与体量
  - logs 表按天分布、单条最大行
  - accounts 表单行体量，以及最大的那个账号各字段占多少

只读打开，不做任何写入。不输出 token / cookie 的值，只输出键名与长度。

用法：
    python3 scripts/db_composition_audit.py
    python3 scripts/db_composition_audit.py --db /tmp/chatgpt2api.db
    python3 scripts/db_composition_audit.py --top 20
"""

import argparse
import json
import os
import sqlite3
import sys


def human(nbytes):
    if nbytes is None:
        return "-"
    value = float(nbytes)
    for unit in ("B", "KB", "MB", "GB"):
        if abs(value) < 1024.0 or unit == "GB":
            return "%.2f %s" % (value, unit)
        value /= 1024.0
    return "%.2f GB" % value


def file_sizes(path):
    out = {}
    for suffix in ("", "-wal", "-shm"):
        p = path + suffix
        out[suffix or "main"] = os.path.getsize(p) if os.path.exists(p) else None
    return out


def page_info(conn):
    page_size = conn.execute("PRAGMA page_size").fetchone()[0]
    page_count = conn.execute("PRAGMA page_count").fetchone()[0]
    freelist = conn.execute("PRAGMA freelist_count").fetchone()[0]
    auto_vacuum = conn.execute("PRAGMA auto_vacuum").fetchone()[0]
    journal = conn.execute("PRAGMA journal_mode").fetchone()[0]
    return {
        "page_size": page_size,
        "page_count": page_count,
        "db_bytes": page_size * page_count,
        "freelist_pages": freelist,
        "freelist_bytes": page_size * freelist,
        "auto_vacuum": auto_vacuum,
        "journal_mode": journal,
    }


def dbstat_objects(conn, limit):
    """dbstat 虚表给出每个表/索引的真实占用；未编译时返回 None。"""
    try:
        rows = conn.execute(
            "SELECT name, SUM(pgsize) AS bytes, COUNT(*) AS pages "
            "FROM dbstat GROUP BY name ORDER BY bytes DESC LIMIT ?",
            (limit,),
        ).fetchall()
    except sqlite3.Error:
        return None
    return [{"name": n, "bytes": b, "pages": p} for n, b, p in rows]


def table_sizes(conn, tables):
    """没有 dbstat 时的退化方案：LENGTH(data) 反映有效载荷（不含页开销）。"""
    out = []
    for table in tables:
        try:
            rows = conn.execute("SELECT COUNT(*) FROM %s" % table).fetchone()[0]
        except sqlite3.Error:
            continue
        try:
            total = conn.execute(
                "SELECT SUM(LENGTH(data)) FROM %s" % table
            ).fetchone()[0] or 0
        except sqlite3.Error:
            total = 0
        out.append({"name": table, "rows": rows, "payload_bytes": total})
    return out


def top_level_prefix(name):
    return name.split("/", 1)[0] if "/" in name else "(root)"


def document_breakdown(conn, top):
    """json_documents 按名称前缀归类。

    生图/缩略图元数据是一图一行；image_tasks.json、
    image_conversation_sessions.json 这类是整表塞进一行。
    """
    try:
        rows = conn.execute(
            "SELECT name, LENGTH(data) AS size FROM json_documents ORDER BY size DESC"
        ).fetchall()
    except sqlite3.Error as exc:
        return {"error": str(exc)}

    groups = {}
    for name, size in rows:
        bucket = groups.setdefault(
            top_level_prefix(name), {"documents": 0, "bytes": 0}
        )
        bucket["documents"] += 1
        bucket["bytes"] += size or 0

    largest = [{"name": n, "bytes": s or 0} for n, s in rows[:top]]
    return {
        "total_documents": len(rows),
        "total_bytes": sum(s or 0 for _, s in rows),
        "by_prefix": [
            {"prefix": k, **v}
            for k, v in sorted(groups.items(), key=lambda kv: -kv[1]["bytes"])
        ],
        "largest": largest,
    }


def inspect_largest_document(conn, name, key_limit=12):
    """看最大的那个文档里装了什么：顶层键、每键体量，以及数组长度。

    只报键名与体量，不报内容。
    """
    try:
        raw = conn.execute(
            "SELECT data FROM json_documents WHERE name = ?", (name,)
        ).fetchone()
    except sqlite3.Error:
        return None
    if not raw:
        return None
    try:
        value = json.loads(raw[0])
    except (TypeError, ValueError):
        return {"bytes": len(raw[0]), "unparsable": True}
    if not isinstance(value, dict):
        return {"bytes": len(raw[0]), "type": type(value).__name__}

    keys = []
    for key, item in value.items():
        size = len(json.dumps(item, ensure_ascii=False))
        entry = {"key": key, "bytes": size, "type": type(item).__name__}
        if isinstance(item, list):
            entry["items"] = len(item)
        keys.append(entry)
    keys.sort(key=lambda kv: -kv["bytes"])
    return {"bytes": len(raw[0]), "top_level_keys": keys[:key_limit]}


def inspect_document_sample(conn, prefix, limit=1, key_limit=8):
    """从某个前缀里取几个样本，看单个文档的字段构成。

    生图元数据每行都有 prompt（完整提示词），这里能量出提示词占多少。
    """
    try:
        rows = conn.execute(
            "SELECT name, LENGTH(data) FROM json_documents "
            "WHERE name LIKE ? ORDER BY LENGTH(data) DESC LIMIT ?",
            (prefix + "/%", limit),
        ).fetchall()
    except sqlite3.Error:
        return []
    out = []
    for name, size in rows:
        entry = {"name": name, "bytes": size or 0}
        try:
            raw = conn.execute(
                "SELECT data FROM json_documents WHERE name = ?", (name,)
            ).fetchone()
            value = json.loads(raw[0]) if raw else None
            if isinstance(value, dict):
                fields = [
                    {
                        "key": k,
                        "bytes": len(json.dumps(v, ensure_ascii=False)),
                    }
                    for k, v in value.items()
                ]
                fields.sort(key=lambda kv: -kv["bytes"])
                entry["fields"] = fields[:key_limit]
        except (sqlite3.Error, TypeError, ValueError):
            pass
        out.append(entry)
    return out


def log_breakdown(conn):
    try:
        total, payload, oldest, newest = conn.execute(
            "SELECT COUNT(*), SUM(LENGTH(data)), MIN(created_at), MAX(created_at) FROM logs"
        ).fetchone()
    except sqlite3.Error as exc:
        return {"error": str(exc)}

    days = []
    try:
        for day, count, nbytes in conn.execute(
            "SELECT day, COUNT(*), SUM(LENGTH(data)) FROM logs "
            "GROUP BY day ORDER BY day DESC LIMIT 30"
        ).fetchall():
            days.append({"day": day, "rows": count, "payload_bytes": nbytes or 0})
    except sqlite3.Error:
        pass

    biggest = []
    try:
        for day, kind, size in conn.execute(
            "SELECT day, type, LENGTH(data) FROM logs ORDER BY LENGTH(data) DESC LIMIT 10"
        ).fetchall():
            biggest.append({"day": day, "type": kind, "bytes": size})
    except sqlite3.Error:
        pass

    # 日志里最占地方的是 request_args（上限 64KB）与 response_body（上限 8KB）。
    with_args = 0
    try:
        with_args = conn.execute(
            "SELECT COUNT(*) FROM logs WHERE data LIKE '%\"request_args\"%'"
        ).fetchone()[0]
    except sqlite3.Error:
        pass

    return {
        "rows": total,
        "payload_bytes": payload or 0,
        "oldest": oldest,
        "newest": newest,
        "rows_with_request_args": with_args,
        "by_day": days,
        "largest_rows": biggest,
    }


def account_breakdown(conn):
    """账号行里最大的是 fp 与 session_cookies。只报键名与长度，不报值。"""
    try:
        sizes = sorted(
            (r[0] or 0) for r in conn.execute("SELECT LENGTH(data) FROM accounts").fetchall()
        )
    except sqlite3.Error as exc:
        return {"error": str(exc)}
    if not sizes:
        return {"rows": 0}

    sample = None
    try:
        raw = conn.execute(
            "SELECT data FROM accounts ORDER BY LENGTH(data) DESC LIMIT 1"
        ).fetchone()
        if raw:
            account = json.loads(raw[0])
            field_bytes = {
                key: len(json.dumps(value, ensure_ascii=False))
                for key, value in account.items()
            }
            sample = {
                "total_bytes": len(raw[0]),
                "field_bytes": dict(sorted(field_bytes.items(), key=lambda kv: -kv[1])),
            }
    except (sqlite3.Error, TypeError, ValueError):
        pass

    return {
        "rows": len(sizes),
        "total_bytes": sum(sizes),
        "min_bytes": sizes[0],
        "median_bytes": sizes[len(sizes) // 2],
        "max_bytes": sizes[-1],
        "largest_account": sample,
    }


def main():
    parser = argparse.ArgumentParser(description="chatgpt2api 数据库空间构成分析（只读）")
    parser.add_argument("--db", help="sqlite 数据库路径")
    parser.add_argument("--root", help="仓库根目录（用于推断 data/chatgpt2api.db）")
    parser.add_argument("--top", type=int, default=15, help="榜单条数，默认 15")
    parser.add_argument("--json-only", action="store_true", help="只输出 JSON")
    args = parser.parse_args()

    path = args.db or os.path.join(args.root or os.getcwd(), "data", "chatgpt2api.db")
    if not os.path.exists(path):
        raise SystemExit("找不到数据库：%s（用 --db 指定）" % path)

    sizes = file_sizes(path)
    try:
        conn = sqlite3.connect("file:%s?mode=ro" % path, uri=True)
    except sqlite3.Error as exc:
        raise SystemExit(
            "只读打开 %s 失败：%s\n"
            "WAL 模式下读连接需要 -shm 可写。可先复制再分析：\n"
            "  cp %s* /tmp/ && python3 %s --db /tmp/%s"
            % (path, exc, path, os.path.basename(__file__), os.path.basename(path))
        )

    try:
        tables = [
            r[0]
            for r in conn.execute(
                "SELECT name FROM sqlite_master WHERE type='table' ORDER BY name"
            ).fetchall()
        ]
        docs = document_breakdown(conn, args.top)
        report = {
            "db_path": path,
            "file_bytes": sizes,
            "pages": page_info(conn),
            "objects": dbstat_objects(conn, args.top),
            "tables": table_sizes(conn, tables),
            "json_documents": docs,
            "logs": log_breakdown(conn),
            "accounts": account_breakdown(conn),
        }
        # 对最大的两个前缀各取一个样本，看清单行构成。
        samples = {}
        if "error" not in docs:
            for group in docs["by_prefix"][:2]:
                prefix = group["prefix"]
                if prefix == "(root)":
                    continue
                samples[prefix] = inspect_document_sample(conn, prefix)
            root_docs = [d for d in docs["largest"] if top_level_prefix(d["name"]) == "(root)"]
            for item in root_docs[:4]:
                samples[item["name"]] = inspect_largest_document(conn, item["name"])
        report["document_samples"] = samples
    finally:
        conn.close()

    if args.json_only:
        print(json.dumps(report, ensure_ascii=False, indent=2))
        return

    print("数据库：%s" % path)
    for suffix, nbytes in sizes.items():
        print("  %-5s %s" % (suffix, human(nbytes)))
    pages = report["pages"]
    print(
        "  页：%d × %d B = %s，空闲 %d 页 (%s)，journal=%s，auto_vacuum=%d"
        % (
            pages["page_count"],
            pages["page_size"],
            human(pages["db_bytes"]),
            pages["freelist_pages"],
            human(pages["freelist_bytes"]),
            pages["journal_mode"],
            pages["auto_vacuum"],
        )
    )

    print("\n== 对象占用（dbstat）==")
    if report["objects"] is None:
        print("  当前 sqlite 未编译 dbstat 虚表，退化为下面的有效载荷统计。")
    else:
        for item in report["objects"]:
            print(
                "  %-32s %12s  %8d 页"
                % (item["name"], human(item["bytes"]), item["pages"])
            )

    print("\n== 各表有效载荷 ==")
    for item in report["tables"]:
        print(
            "  %-24s rows=%-8d payload=%s"
            % (item["name"], item["rows"], human(item["payload_bytes"]))
        )

    docs = report["json_documents"]
    print("\n== json_documents 分类 ==")
    if "error" in docs:
        print("  %s" % docs["error"])
    else:
        print(
            "  共 %d 个文档，有效载荷 %s"
            % (docs["total_documents"], human(docs["total_bytes"]))
        )
        for item in docs["by_prefix"]:
            print(
                "    %-32s %8d 个  %12s"
                % (item["prefix"], item["documents"], human(item["bytes"]))
            )
        print("  最大的文档：")
        for item in docs["largest"]:
            print("    %-56s %12s" % (item["name"], human(item["bytes"])))

    if report["document_samples"]:
        print("\n== 文档样本（看单行里装了什么）==")
        for name, sample in report["document_samples"].items():
            print("  %s" % name)
            if isinstance(sample, list):
                for entry in sample:
                    print("    %s  %s" % (entry["name"], human(entry["bytes"])))
                    for field in entry.get("fields", []):
                        print(
                            "      %-28s %12s" % (field["key"], human(field["bytes"]))
                        )
            elif isinstance(sample, dict):
                print("    总 %s" % human(sample.get("bytes")))
                for field in sample.get("top_level_keys", []):
                    extra = (
                        " (%d 项)" % field["items"] if "items" in field else ""
                    )
                    print(
                        "      %-28s %12s%s"
                        % (field["key"], human(field["bytes"]), extra)
                    )

    logs = report["logs"]
    print("\n== 日志 ==")
    if "error" in logs:
        print("  %s" % logs["error"])
    else:
        print(
            "  %d 行，有效载荷 %s，时间范围 %s ~ %s"
            % (logs["rows"], human(logs["payload_bytes"]), logs["oldest"], logs["newest"])
        )
        print("  含 request_args 的行：%d" % logs["rows_with_request_args"])
        for item in logs["by_day"][:14]:
            print(
                "    %-12s %8d 行  %12s"
                % (item["day"], item["rows"], human(item["payload_bytes"]))
            )
        if logs["largest_rows"]:
            print("  最大的日志行：")
            for item in logs["largest_rows"]:
                print(
                    "    %-12s %-10s %12s"
                    % (item["day"], item["type"], human(item["bytes"]))
                )

    accounts = report["accounts"]
    print("\n== accounts ==")
    if accounts.get("rows", 0) == 0:
        print("  无账号")
    else:
        print(
            "  %d 行，合计 %s，单行 min=%s / median=%s / max=%s"
            % (
                accounts["rows"],
                human(accounts["total_bytes"]),
                human(accounts["min_bytes"]),
                human(accounts["median_bytes"]),
                human(accounts["max_bytes"]),
            )
        )
        sample = accounts.get("largest_account")
        if sample:
            print("  最大账号的字段体量：")
            for key, nbytes in sample["field_bytes"].items():
                print("    %-24s %12s" % (key, human(nbytes)))

    print("\n完整 JSON：加 --json-only")


if __name__ == "__main__":
    main()
