#!/usr/bin/env python3
"""只读诊断脚本：审计账号池的浏览器身份自洽性与 Cloudflare cookie 新鲜度。

回答的问题：
  - 哪些账号的 UA / Client-Hints / TLS profile 互相矛盾（CF 能识别的身份撕裂）
  - 哪些账号的 cf_clearance 已过窗口、即将被丢弃，或压根没有时间戳
  - 导入账号的 oai-did cookie 是否与指纹里的 oai-device-id 一致
  - access token 是否已过期

设计约束：**绝不输出任何密文值**。
access_token / session_token / cookie 值 / 代理 URL / 邮箱本地部分
一律只输出「是否存在」「哈希前缀」「年龄」这类派生信息。

用法：
    python3 scripts/account_identity_audit.py                # 表格 + JSON
    python3 scripts/account_identity_audit.py --csv          # 一行一账号，便于贴表对比
    python3 scripts/account_identity_audit.py --json-only > audit.json
    python3 scripts/account_identity_audit.py --db /app/data/chatgpt2api.db

脚本会从自身位置向上找 go.mod / .env 推断仓库根，再读 .env 里的 DATABASE_URL。
后台库（postgres / mysql）不受支持，请改用：
    SELECT data FROM accounts;   然后把结果喂给 --stdin
"""

import argparse
import base64
import hashlib
import json
import os
import re
import sqlite3
import sys
from datetime import datetime, timedelta, timezone

# 与 internal/service/session_cookies.go 的常量保持一致。
CLEARANCE_WINDOW = timedelta(hours=2)
TOKEN_WINDOW = timedelta(minutes=30)

# 与 internal/service/session_cookies.go:isAllowedSessionCookieName 保持一致。
CF_COOKIE_NAMES = {"cf_clearance", "__cf_bm", "__cflb", "_cfuvid"}


def cookie_window(name, stable_exit_ip):
    """返回 (窗口, 是否受限)。与 cloudflareCookieFreshWindow 同语义。"""
    if name == "cf_clearance":
        return (None, False) if stable_exit_ip else (CLEARANCE_WINDOW, True)
    if name == "__cf_bm":
        return TOKEN_WINDOW, True
    if name in ("_cfuvid", "__cflb"):
        return None, False
    if name.startswith("cf_chl_"):
        return (None, False) if stable_exit_ip else (CLEARANCE_WINDOW, True)
    if name.startswith("__cf"):
        return TOKEN_WINDOW, True
    return None, False


def is_cf_cookie(name):
    return (
        name in CF_COOKIE_NAMES
        or name.startswith("cf_chl_")
        or name.startswith("__cf")
    )


def sha1_short(text, n=16):
    return hashlib.sha1(text.encode("utf-8")).hexdigest()[:n]


def token_preview(token):
    if not token:
        return "token:empty"
    return "token:" + hashlib.sha256(token.encode("utf-8")).hexdigest()[:10]


def mask_email(value):
    """只保留域名，本地部分缩短为 1 个字符 + ***。"""
    value = (value or "").strip()
    if "@" not in value:
        return None
    local, _, domain = value.partition("@")
    return (local[:1] or "?") + "***@" + domain


def parse_ts(value):
    if not value or not isinstance(value, str):
        return None
    text = value.strip().replace("Z", "+00:00")
    try:
        parsed = datetime.fromisoformat(text)
    except ValueError:
        return None
    if parsed.tzinfo is None:
        parsed = parsed.replace(tzinfo=timezone.utc)
    return parsed.astimezone(timezone.utc)


def decode_jwt_claims(token):
    """解码 JWT payload（不验签）。只取时间与套餐类型这类非密文字段。"""
    parts = (token or "").split(".")
    if len(parts) < 2:
        return {}
    payload = parts[1]
    payload += "=" * (-len(payload) % 4)
    try:
        raw = base64.urlsafe_b64decode(payload)
        claims = json.loads(raw)
    except Exception:
        return {}
    if not isinstance(claims, dict):
        return {}
    return claims


def ua_major(user_agent, pattern):
    match = re.search(pattern, user_agent or "")
    return match.group(1).split(".")[0] if match else ""


def brand_version(sec_ch_ua, *brands):
    """从 Sec-Ch-Ua 里取出指定品牌的 v= 值。

    不能取第一个 v=：Chrome 的 Sec-Ch-Ua 首项恒为 `"Not:A-Brand";v="99"`，
    取首个匹配会把 99 误当成浏览器大版本。
    """
    text = sec_ch_ua or ""
    for brand in brands:
        match = re.search(
            r'"%s";v="([0-9]+)' % re.escape(brand), text, re.IGNORECASE
        )
        if match:
            return match.group(1)
    return ""


def identity_snapshot(fp):
    """从指纹里抽出身份三元组，并判定它们是否同源。"""
    user_agent = str(fp.get("user-agent") or "")
    sec_ch_ua = str(fp.get("sec-ch-ua") or "")
    full_version = str(fp.get("sec-ch-ua-full-version") or "")
    full_version_list = str(fp.get("sec-ch-ua-full-version-list") or "")
    impersonate = str(fp.get("impersonate") or "")

    ua_chrome = ua_major(user_agent, r"Chrome/([0-9]+(?:\.[0-9]+){0,3})")
    ua_firefox = ua_major(user_agent, r"Firefox/([0-9]+(?:\.[0-9]+){0,3})")
    ua_edge = ua_major(user_agent, r"Edg[A-Z]*/([0-9]+(?:\.[0-9]+){0,3})")
    # client-hints 的品牌顺序因浏览器而异，按品牌名精确取，不能取首个 v=。
    ch_chrome = brand_version(sec_ch_ua, "Google Chrome")
    ch_firefox = brand_version(sec_ch_ua, "Firefox")
    ch_edge = brand_version(sec_ch_ua, "Microsoft Edge")

    # TLS profile 只有 surf 能兑现的两套：chrome145 / firefox148。
    # impersonate 里的版本号会被 surf 静默丢弃，所以只比族。
    profile_family = ""
    if impersonate.startswith("chrome"):
        profile_family = "chrome"
    elif impersonate.startswith("firefox"):
        profile_family = "firefox"
    elif impersonate.startswith("edge"):
        profile_family = "edge"
    elif impersonate.startswith("safari"):
        profile_family = "safari"

    declared_family = str(fp.get("browser-family") or "")
    ua_family = ""
    if ua_edge:
        ua_family = "edge"
    elif ua_firefox:
        ua_family = "firefox"
    elif ua_chrome:
        ua_family = "chrome"

    ua_all_major = ""
    if ua_family == "firefox":
        ua_all_major = ua_firefox
    elif ua_family == "edge":
        ua_all_major = ua_edge
    elif ua_family == "chrome":
        ua_all_major = ua_chrome

    # Client-Hints 声明的族：与 UA 族应当一致。
    ch_family = ""
    if ch_edge:
        ch_family = "edge"
    elif ch_firefox:
        ch_family = "firefox"
    elif ch_chrome:
        ch_family = "chrome"
    ch_major = {"firefox": ch_firefox, "edge": ch_edge, "chrome": ch_chrome}.get(
        ch_family, ""
    )

    # 一致 = UA / Sec-Ch-Ua / 声明族 / TLS profile 族 四者同源。
    consistent = bool(ua_family) and (
        ua_family == declared_family == profile_family == ch_family
        and (not ch_major or ch_major == ua_all_major)
    )

    # TLS 层实际兑现的族：surf 只认 chrome / firefox，edge 与 safari 落 chrome。
    tls_family = "firefox" if profile_family == "firefox" else "chrome"

    return {
        "user_agent": user_agent or None,
        "sec_ch_ua": sec_ch_ua or None,
        "sec_ch_ua_full_version": full_version or None,
        "sec_ch_ua_full_version_list": full_version_list or None,
        "impersonate": impersonate or None,
        "declared_family": declared_family or None,
        "ua_family": ua_family or None,
        "ua_major": ua_all_major or None,
        "ch_family": ch_family or None,
        "ch_major": ch_major or None,
        "tls_family": tls_family,
        "identity_consistent": consistent,
        # full-version 与 UA 大版本不一致 = 上游能看到的自相矛盾信号
        "full_version_matches_ua": (
            not full_version or not ua_all_major
            or full_version.strip('"').split(".")[0] == ua_all_major
        ),
    }


def is_allowed_cookie_name(name):
    """与 internal/service/session_cookies.go:isAllowedSessionCookieName 保持一致。"""
    if name in (
        "__Secure-next-auth.session-token",
        "__Secure-next-auth.callback-url",
        "__Host-next-auth.csrf-token",
        "cf_clearance",
        "__cf_bm",
        "__cflb",
        "_cfuvid",
        "_puid",
        "_account_is_fedramp",
        "oai-did",
        "oai-sc",
        "oai-chat-web-route",
        "oai-client-auth-info",
        "oai-gn",
        "oai-hlib",
        "__Secure-oai-is",
    ):
        return True
    return (
        name.startswith("__Secure-next-auth.session-token.")
        or name.startswith("cf_chl_")
        or name.startswith("__cf")
    )


def audit_cookies(account, now):
    """复算 AccountSessionCookiesForRequest 的判定：哪些 CF cookie 会被发出去。"""
    raw = account.get("session_cookies")
    cookies = {}
    if isinstance(raw, dict):
        for name, value in raw.items():
            # 与 SessionCookieStringMap 一样先过白名单，否则会把
            # "__Host-next-auth.csrf-token" 这类未支持 cookie 也算进统计。
            if isinstance(value, str) and value.strip() and is_allowed_cookie_name(
                str(name).strip()
            ):
                cookies[str(name).strip()] = True
    elif isinstance(raw, list):
        for item in raw:
            if isinstance(item, dict):
                name = str(item.get("name") or "").strip()
                if name and item.get("value") and is_allowed_cookie_name(name):
                    cookies[name] = True

    stamps = {}
    raw_stamps = account.get("session_cookie_updated_at")
    if isinstance(raw_stamps, dict):
        for name, value in raw_stamps.items():
            stamps[str(name)] = value

    stable_exit_ip = bool(str(account.get("proxy") or "").strip())
    details = {}
    for name in sorted(cookies):
        if not is_cf_cookie(name):
            continue
        window, limited = cookie_window(name, stable_exit_ip)
        age_hours = None
        sent = True
        reason = "no-window"
        if limited:
            updated = parse_ts(stamps.get(name))
            if updated is None:
                sent = False
                reason = "missing-timestamp"
            else:
                age = now - updated
                age_hours = round(age.total_seconds() / 3600.0, 2)
                if age > window:
                    sent = False
                    reason = "stale"
                else:
                    reason = "fresh"
        details[name] = {
            "present": True,
            "age_hours": age_hours,
            "would_send": sent,
            "reason": reason,
        }

    session_token_cookie = any(
        n == "__Secure-next-auth.session-token"
        or n.startswith("__Secure-next-auth.session-token.")
        for n in cookies
    )

    return {
        "cookie_names": sorted(cookies),
        "cf_cookies": details,
        "has_oai_did_cookie": "oai-did" in cookies,
        "has_session_token_cookie": session_token_cookie,
        "stable_exit_ip": stable_exit_ip,
    }


def audit_account(account, now, row_id):
    token = str(account.get("access_token") or "")
    fp = account.get("fp")
    if not isinstance(fp, dict):
        fp = {}

    identity = identity_snapshot(fp)
    cookies = audit_cookies(account, now)

    claims = decode_jwt_claims(token)
    exp = claims.get("exp")
    iat = claims.get("iat")
    exp_dt = None
    iat_dt = None
    if isinstance(exp, (int, float)):
        exp_dt = datetime.fromtimestamp(exp, tz=timezone.utc)
    if isinstance(iat, (int, float)):
        iat_dt = datetime.fromtimestamp(iat, tz=timezone.utc)

    # 导入账号的关键裂痕：cookie 里的 oai-did 与指纹里的 oai-device-id 是否同一设备。
    # 不同 = 「cookie 指向设备 A、UA 与 OAI-Device-Id 指向设备 B」。
    oai_did_matches = None
    raw_cookies = account.get("session_cookies")
    if isinstance(raw_cookies, dict) and fp.get("oai-device-id"):
        cookie_did = raw_cookies.get("oai-did")
        if isinstance(cookie_did, str) and cookie_did.strip():
            oai_did_matches = cookie_did.strip() == str(fp["oai-device-id"]).strip()

    return {
        "db_row_id": row_id,
        "account_id": sha1_short(token) if token else None,
        "token_preview": token_preview(token),
        "type": account.get("type"),
        "status": account.get("status"),
        "quota": account.get("quota"),
        "image_quota_unknown": account.get("image_quota_unknown"),
        "restore_at": account.get("restore_at"),
        "email": mask_email(account.get("email")),
        "success": account.get("success"),
        "fail": account.get("fail"),
        "has_session_token": bool(str(account.get("session_token") or "").strip()),
        "has_proxy_binding": bool(str(account.get("proxy") or "").strip()),
        "token_issued_at": iat_dt.isoformat() if iat_dt else None,
        "token_expires_at": exp_dt.isoformat() if exp_dt else None,
        "token_expired": bool(exp_dt and exp_dt <= now),
        "token_days_to_expiry": (
            round((exp_dt - now).total_seconds() / 86400.0, 1) if exp_dt else None
        ),
        "plan_type": (
            (claims.get("https://api.openai.com/auth") or {}).get("chatgpt_plan_type")
            if isinstance(claims.get("https://api.openai.com/auth"), dict)
            else None
        ),
        "fp_version": fp.get("version"),
        "fp_keys": sorted(fp.keys()),
        "browser_family": fp.get("browser-family"),
        "browser_version": fp.get("browser-version"),
        "top_level_family": account.get("browser-family"),
        "top_level_version": account.get("browser-version"),
        "oai_did_matches_fp_device": oai_did_matches,
        "identity": identity,
        "cookies": cookies,
    }


def summarize(rows, now):
    total = len(rows)
    summary = {
        "generated_at": now.isoformat(),
        "account_count": total,
        "identity_inconsistent": sum(
            1 for r in rows if not r["identity"]["identity_consistent"]
        ),
        "full_version_mismatch": sum(
            1 for r in rows if not r["identity"]["full_version_matches_ua"]
        ),
        "token_expired": sum(1 for r in rows if r["token_expired"]),
        "oai_did_mismatch": sum(
            1 for r in rows if r["oai_did_matches_fp_device"] is False
        ),
        "oai_did_unknown": sum(
            1 for r in rows if r["oai_did_matches_fp_device"] is None
        ),
        "proxy_bound": sum(1 for r in rows if r["has_proxy_binding"]),
        "clearance_sent": 0,
        "clearance_dropped_stale": 0,
        "clearance_dropped_no_timestamp": 0,
        "clearance_absent": 0,
        "bm_dropped": 0,
        "family_version_counts": {},
        "status_counts": {},
    }
    for r in rows:
        key = "%s%s" % (
            r["browser_family"] or "?",
            r["browser_version"] or "",
        )
        summary["family_version_counts"][key] = (
            summary["family_version_counts"].get(key, 0) + 1
        )
        status = r["status"] or "?"
        summary["status_counts"][status] = summary["status_counts"].get(status, 0) + 1

        cf = r["cookies"]["cf_cookies"]
        clearance = cf.get("cf_clearance")
        if clearance is None:
            summary["clearance_absent"] += 1
        elif clearance["would_send"]:
            summary["clearance_sent"] += 1
        elif clearance["reason"] == "missing-timestamp":
            summary["clearance_dropped_no_timestamp"] += 1
        else:
            summary["clearance_dropped_stale"] += 1

        bm = cf.get("__cf_bm")
        if bm is not None and not bm["would_send"]:
            summary["bm_dropped"] += 1
    return summary


def print_table(rows):
    header = (
        "%-16s %-6s %-6s %-5s %-12s %-6s %-11s %-9s %-7s %-9s %s"
        % (
            "account_id",
            "family",
            "ver",
            "self#",
            "cf_clearance",
            "cf_bm",
            "did_match",
            "expired",
            "quota",
            "status",
            "consist",
        )
    )
    print(header)
    print("-" * len(header))
    for r in rows:
        cf = r["cookies"]["cf_cookies"]
        clearance = cf.get("cf_clearance")
        if clearance is None:
            clearance_text = "-"
        elif clearance["would_send"]:
            age = clearance["age_hours"]
            clearance_text = "sent" if age is None else "sent/%sh" % age
        else:
            clearance_text = clearance["reason"]
        bm = cf.get("__cf_bm")
        bm_text = "-"
        if bm is not None:
            bm_text = "sent" if bm["would_send"] else bm["reason"]
        did = r["oai_did_matches_fp_device"]
        did_text = "?" if did is None else ("ok" if did else "MISMATCH")
        print(
            "%-16s %-6s %-6s %-5s %-12s %-6s %-11s %-9s %-7s %-9s %s"
            % (
                r["account_id"] or "-",
                r["browser_family"] or "?",
                r["browser_version"] or "-",
                "ok" if r["identity"]["identity_consistent"] else "BAD",
                clearance_text,
                bm_text,
                did_text,
                "yes" if r["token_expired"] else "no",
                r["quota"],
                r["status"],
                "ok" if r["identity"]["full_version_matches_ua"] else "BAD",
            )
        )


def load_rows_from_stdin():
    raw = sys.stdin.read()
    try:
        payload = json.loads(raw)
    except json.JSONDecodeError:
        rows = []
        for line in raw.splitlines():
            line = line.strip().rstrip(",")
            if not line:
                continue
            rows.append(json.loads(line))
        return rows
    if isinstance(payload, list):
        return payload
    if isinstance(payload, dict) and isinstance(payload.get("items"), list):
        return payload["items"]
    raise SystemExit("stdin 不是账号数组，也不是 {items: [...]}")


def resolve_sqlite_path(root, explicit):
    if explicit:
        return explicit
    dsn = (os.environ.get("DATABASE_URL") or "").strip()
    if root:
        env_path = os.path.join(root, ".env")
        if os.path.exists(env_path):
            try:
                with open(env_path, "r", encoding="utf-8") as handle:
                    for line in handle:
                        line = line.strip()
                        if line.startswith("DATABASE_URL=") and not dsn:
                            dsn = line.split("=", 1)[1].strip().strip('"').strip("'")
            except OSError:
                pass
    for prefix in ("sqlite:///", "sqlite://", "sqlite:"):
        if dsn.startswith(prefix):
            path = dsn[len(prefix):]
            if path:
                return path
    if dsn and "://" in dsn:
        raise SystemExit(
            "DATABASE_URL 指向非 sqlite 后端（%s）。\n"
            "请执行 `SELECT data FROM accounts;` 并把结果通过 --stdin 传入。"
            % dsn.split("://", 1)[0]
        )
    base = root or os.getcwd()
    return os.path.join(base, "data", "chatgpt2api.db")


def find_root(start):
    current = os.path.abspath(start)
    while True:
        if os.path.exists(os.path.join(current, "go.mod")) or os.path.exists(
            os.path.join(current, ".env")
        ):
            return current
        parent = os.path.dirname(current)
        if parent == current:
            return None
        current = parent


def print_csv(rows):
    """输出扁平 CSV：一行一账号，便于把失败组与正常组贴在一起对比。"""
    import csv as csv_module

    columns = [
        "account_id",
        "row_id",
        "type",
        "status",
        "quota",
        "image_quota_unknown",
        "restore_at",
        "success",
        "fail",
        "has_session_token",
        "has_proxy_binding",
        "browser_family",
        "browser_version",
        "impersonate",
        "ua_family",
        "ua_major",
        "ch_family",
        "ch_major",
        "tls_family",
        "identity_consistent",
        "full_version_matches_ua",
        "oai_did_matches_fp_device",
        "has_oai_did_cookie",
        "has_session_token_cookie",
        "cf_clearance_present",
        "cf_clearance_age_hours",
        "cf_clearance_would_send",
        "cf_clearance_reason",
        "cf_bm_present",
        "cf_bm_age_hours",
        "cf_bm_would_send",
        "cf_cookie_names",
        "token_expires_at",
        "token_expired",
        "token_days_to_expiry",
        "plan_type",
    ]
    writer = csv_module.writer(sys.stdout, lineterminator="\n")
    writer.writerow(columns)
    for r in rows:
        cf = r["cookies"]["cf_cookies"]
        clearance = cf.get("cf_clearance") or {}
        bm = cf.get("__cf_bm") or {}
        writer.writerow(
            [
                r["account_id"],
                r["db_row_id"],
                r["type"],
                r["status"],
                r["quota"],
                r["image_quota_unknown"],
                r["restore_at"],
                r["success"],
                r["fail"],
                r["has_session_token"],
                r["has_proxy_binding"],
                r["browser_family"],
                r["browser_version"],
                r["identity"]["impersonate"],
                r["identity"]["ua_family"],
                r["identity"]["ua_major"],
                r["identity"]["ch_family"],
                r["identity"]["ch_major"],
                r["identity"]["tls_family"],
                r["identity"]["identity_consistent"],
                r["identity"]["full_version_matches_ua"],
                r["oai_did_matches_fp_device"],
                r["cookies"]["has_oai_did_cookie"],
                r["cookies"]["has_session_token_cookie"],
                bool(clearance.get("present")),
                clearance.get("age_hours"),
                clearance.get("would_send"),
                clearance.get("reason"),
                bool(bm.get("present")),
                bm.get("age_hours"),
                bm.get("would_send"),
                "|".join(r["cookies"]["cookie_names"]),
                r["token_expires_at"],
                r["token_expired"],
                r["token_days_to_expiry"],
                r["plan_type"],
            ]
        )


def main():
    parser = argparse.ArgumentParser(description="账号身份与 CF cookie 只读审计")
    parser.add_argument("--db", help="sqlite 数据库路径（默认从 .env / 仓库结构推断）")
    parser.add_argument(
        "--root", help="仓库根目录（默认从脚本位置向上查找 go.mod / .env）"
    )
    parser.add_argument(
        "--stdin", action="store_true", help="从标准输入读取账号 JSON 数组（非 sqlite 后端）"
    )
    parser.add_argument("--json-only", action="store_true", help="只输出 JSON")
    parser.add_argument("--csv", action="store_true", help="只输出 CSV（便于贴表对比）")
    args = parser.parse_args()

    now = datetime.now(timezone.utc)

    if args.stdin:
        rows = [audit_account(a, now, i) for i, a in enumerate(load_rows_from_stdin())]
    else:
        root = args.root or find_root(os.path.dirname(os.path.abspath(__file__)))
        db_path = resolve_sqlite_path(root, args.db)
        if not os.path.exists(db_path):
            raise SystemExit("找不到数据库：%s（用 --db 指定）" % db_path)
        try:
            # 只读打开；WAL 模式下读连接仍需可写的 -shm，失败时给出替代方案。
            conn = sqlite3.connect("file:%s?mode=ro" % db_path, uri=True)
            cur = conn.execute("SELECT id, data FROM accounts")
            raw_rows = cur.fetchall()
        except sqlite3.Error as exc:
            raise SystemExit(
                "只读打开 %s 失败：%s\n"
                "WAL 模式下读连接需要 -shm 可写。可复制数据库后离线分析：\n"
                "  cp %s* /tmp/ && python3 %s --db /tmp/%s"
                % (
                    db_path,
                    exc,
                    db_path,
                    os.path.basename(__file__),
                    os.path.basename(db_path),
                )
            )
        finally:
            try:
                conn.close()
            except Exception:
                pass

        rows = []
        for row_id, data in raw_rows:
            try:
                account = json.loads(data)
            except (TypeError, json.JSONDecodeError):
                continue
            if isinstance(account, dict):
                rows.append(audit_account(account, now, row_id))

    if args.csv:
        print_csv(rows)
        return

    report = {"summary": summarize(rows, now), "accounts": rows}
    if not args.json_only:
        print_table(rows)
        print()
    print(json.dumps(report, ensure_ascii=False, indent=2, sort_keys=False))


if __name__ == "__main__":
    main()
