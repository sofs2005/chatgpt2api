r"""
ChatGPT gpt-image-2 完整生图链路脚本
基于 F:/chatgpt2api 原项目逆向的完整流程:
  Bootstrap -> Chat-Requirements -> PoW -> Prepare -> Generate (SSE) -> Download
使用 curl-cffi + edge101 指纹绕过 Cloudflare WAF
"""
import base64
import hashlib
import json
import random
import re
import time
import uuid
from datetime import datetime, timedelta, timezone
from html.parser import HTMLParser
from io import BytesIO
from pathlib import Path
from typing import Any, Iterator

import pybase64
from curl_cffi import requests
# PIL only needed for image upload; text-only prompts don't need it

# ============ 配置 ============
ACCESS_TOKEN = "YOUR_ACCESS_TOKEN_HERE"

PROMPT = "A serene Japanese zen garden with cherry blossoms falling, golden hour lighting, photorealistic"
BASE_URL = "https://chatgpt.com"
OUTPUT_DIR = Path("jshook/responses")
OUTPUT_DIR.mkdir(parents=True, exist_ok=True)

CLIENT_VERSION = "prod-be885abbfcfe7b1f511e88b3003d9ee44757fbad"
CLIENT_BUILD_NUMBER = "5955942"
POW_SCRIPT_DEFAULT = "https://chatgpt.com/backend-api/sentinel/sdk.js"


# ============ 工具函数 ============
def new_uuid() -> str:
    return str(uuid.uuid4())


def ensure_ok(response, context: str) -> None:
    if 200 <= response.status_code < 300:
        return
    try:
        body = response.json()
    except Exception:
        body = response.text[:500]
    raise RuntimeError(f"{context} failed: HTTP {response.status_code}, body={body}")


# ============ 指纹/设备信息 ============
FINGERPRINT = {
    "user-agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36 Edg/143.0.0.0",
    "impersonate": "edge101",
    "sec-ch-ua": '"Microsoft Edge";v="143", "Chromium";v="143", "Not A(Brand";v="24"',
    "sec-ch-ua-mobile": "?0",
    "sec-ch-ua-platform": '"Windows"',
}
DEVICE_ID = new_uuid()
SESSION_ID = new_uuid()


# ============ PoW 实现 (从原项目移植) ============
class ScriptSrcParser(HTMLParser):
    def __init__(self):
        super().__init__()
        self.script_sources: list[str] = []
        self.data_build = ""

    def handle_starttag(self, tag, attrs):
        if tag != "script":
            return
        attrs_dict = dict(attrs)
        src = attrs_dict.get("src")
        if not src:
            return
        self.script_sources.append(src)
        match = re.search(r"c/[^/]*/_", src)
        if match:
            self.data_build = match.group(0)


def parse_pow_resources(html: str) -> tuple[list[str], str]:
    parser = ScriptSrcParser()
    parser.feed(html)
    sources = parser.script_sources or [POW_SCRIPT_DEFAULT]
    data_build = parser.data_build
    if not data_build:
        match = re.search(r'<html[^>]*data-build="([^"]*)"', html)
        if match:
            data_build = match.group(1)
    return sources, data_build


CORES = [8, 16, 24, 32]
DOCUMENT_KEYS = ["_reactListeningo743lnnpvdg", "location"]


def _legacy_parse_time() -> str:
    now = datetime.now(timezone(timedelta(hours=-5)))
    return now.strftime("%a %b %d %Y %H:%M:%S") + " GMT-0500 (Eastern Standard Time)"


def build_pow_config(user_agent, script_sources, data_build):
    navigator_key = random.choice([
        "registerProtocolHandler−function registerProtocolHandler() { [native code] }",
        "storage−[object StorageManager]",
        "webdriver−false",
        "hardwareConcurrency−32",
        "vendor−Google Inc.",
        "cookieEnabled−true",
        "language−zh-CN",
        "pdfViewerEnabled−true",
        "product−Gecko",
        "onLine−true",
    ])
    window_key = random.choice([
        "0", "window", "self", "document", "location", "innerWidth",
        "innerHeight", "navigator", "performance", "crypto", "indexedDB",
        "localStorage", "sessionStorage", "fetch", "setTimeout",
    ])
    script_source = random.choice(list(script_sources)) if script_sources else POW_SCRIPT_DEFAULT
    return [
        random.choice([3000, 4000, 5000]),
        _legacy_parse_time(),
        4294705152, 0,
        user_agent, script_source, data_build,
        "en-US", "en-US,es-US,en,es", 0,
        navigator_key,
        random.choice(DOCUMENT_KEYS),
        window_key,
        time.perf_counter() * 1000,
        new_uuid(), "",
        random.choice(CORES),
        time.time() * 1000 - (time.perf_counter() * 1000),
    ]


def pow_generate(seed: str, difficulty: str, config: list, limit: int = 500000) -> tuple[str, bool]:
    target = bytes.fromhex(difficulty)
    diff_len = len(difficulty) // 2
    seed_bytes = seed.encode()
    static_1 = (json.dumps(config[:3], separators=(",", ":"), ensure_ascii=False)[:-1] + ",").encode()
    static_2 = ("," + json.dumps(config[4:9], separators=(",", ":"), ensure_ascii=False)[1:-1] + ",").encode()
    static_3 = ("," + json.dumps(config[10:], separators=(",", ":"), ensure_ascii=False)[1:]).encode()
    for i in range(limit):
        final_json = static_1 + str(i).encode() + static_2 + str(i >> 1).encode() + static_3
        encoded = pybase64.b64encode(final_json)
        digest = hashlib.sha3_512(seed_bytes + encoded).digest()
        if digest[:diff_len] <= target:
            return encoded.decode(), True
    fallback = "wQ8Lk5FbGpA2NcR9dShT6gYjU7VxZ4D" + pybase64.b64encode(f'"{seed}"'.encode()).decode()
    return fallback, False


def build_legacy_requirements_token(user_agent, script_sources, data_build) -> str:
    seed = format(random.random())
    config = build_pow_config(user_agent, script_sources, data_build)
    answer, _ = pow_generate(seed, "0fffff", config)
    return "gAAAAAC" + answer


def build_proof_token(seed, difficulty, user_agent, script_sources, data_build) -> str:
    config = build_pow_config(user_agent, script_sources, data_build)
    answer, solved = pow_generate(seed, difficulty, config)
    if not solved:
        raise RuntimeError(f"Failed to solve proof token: difficulty={difficulty}")
    return "gAAAAAB" + answer


# ============ Turnstile 求解器 (从原项目移植) ============
class OrderedMap:
    def __init__(self):
        self.keys = []
        self.values = {}

    def add(self, key, value):
        if key not in self.values:
            self.keys.append(key)
        self.values[key] = value


def _turnstile_to_str(value):
    if value is None:
        return "undefined"
    if isinstance(value, float):
        return str(value)
    if isinstance(value, str):
        special = {
            "window.Math": "[object Math]",
            "window.Reflect": "[object Reflect]",
            "window.performance": "[object Performance]",
            "window.localStorage": "[object Storage]",
            "window.Object": "function Object() { [native code] }",
            "window.Reflect.set": "function set() { [native code] }",
            "window.performance.now": "function () { [native code] }",
            "window.Object.create": "function create() { [native code] }",
            "window.Object.keys": "function keys() { [native code] }",
            "window.Math.random": "function random() { [native code] }",
        }
        return special.get(value, value)
    if isinstance(value, list) and all(isinstance(item, str) for item in value):
        return ",".join(value)
    return str(value)


def _xor_string(text, key):
    if not key:
        return text
    return "".join(chr(ord(ch) ^ ord(key[i % len(key)])) for i, ch in enumerate(text))


def solve_turnstile_token(dx: str, p: str) -> str | None:
    try:
        decoded = base64.b64decode(dx).decode()
        token_list = json.loads(_xor_string(decoded, p))
    except Exception:
        return None

    process_map = {}
    start_time = time.time()
    result = ""

    def func_1(e, t):
        process_map[e] = _xor_string(_turnstile_to_str(process_map[e]), _turnstile_to_str(process_map[t]))

    def func_2(e, t):
        process_map[e] = t

    def func_3(e):
        nonlocal result
        result = base64.b64encode(e.encode()).decode()

    def func_5(e, t):
        current = process_map[e]
        incoming = process_map[t]
        if isinstance(current, (list, tuple)):
            process_map[e] = list(current) + [incoming]
            return
        if isinstance(current, (str, float)) or isinstance(incoming, (str, float)):
            process_map[e] = _turnstile_to_str(current) + _turnstile_to_str(incoming)

    def func_6(e, t, n):
        tv = process_map[t]
        nv = process_map[n]
        if isinstance(tv, str) and isinstance(nv, str):
            value = f"{tv}.{nv}"
            process_map[e] = "https://chatgpt.com/" if value == "window.document.location" else value

    def func_7(e, *args):
        target = process_map[e]
        values = [process_map[arg] for arg in args]
        if isinstance(target, str) and target == "window.Reflect.set":
            obj, key_name, val = values
            obj.add(str(key_name), val)
        elif callable(target):
            target(*values)

    def func_8(e, t):
        process_map[e] = process_map[t]

    def func_14(e, t):
        process_map[e] = json.loads(process_map[t])

    def func_15(e, t):
        process_map[e] = json.dumps(process_map[t])

    def func_17(e, t, *args):
        call_args = [process_map[arg] for arg in args]
        target = process_map[t]
        if target == "window.performance.now":
            elapsed_ns = time.time_ns() - int(start_time * 1e9)
            process_map[e] = (elapsed_ns + random.random()) / 1e6
        elif target == "window.Object.create":
            process_map[e] = OrderedMap()
        elif target == "window.Object.keys":
            if call_args and call_args[0] == "window.localStorage":
                process_map[e] = [
                    "STATSIG_LOCAL_STORAGE_INTERNAL_STORE_V4",
                    "STATSIG_LOCAL_STORAGE_STABLE_ID",
                    "client-correlated-secret",
                    "oai/apps/capExpiresAt",
                    "oai-did",
                    "STATSIG_LOCAL_STORAGE_LOGGING_REQUEST",
                    "UiState.isNavigationCollapsed.1",
                ]
        elif target == "window.Math.random":
            process_map[e] = random.random()
        elif callable(target):
            process_map[e] = target(*call_args)

    def func_18(e):
        process_map[e] = base64.b64decode(_turnstile_to_str(process_map[e])).decode()

    def func_19(e):
        process_map[e] = base64.b64encode(_turnstile_to_str(process_map[e]).encode()).decode()

    def func_20(e, t, n, *args):
        if process_map[e] == process_map[t]:
            target = process_map[n]
            if callable(target):
                target(*[process_map[arg] for arg in args])

    def func_21(*_):
        return

    def func_23(e, t, *args):
        if process_map[e] is not None and callable(process_map[t]):
            process_map[t](*args)

    def func_24(e, t, n):
        tv = process_map[t]
        nv = process_map[n]
        if isinstance(tv, str) and isinstance(nv, str):
            process_map[e] = f"{tv}.{nv}"

    process_map.update({
        1: func_1, 2: func_2, 3: func_3, 5: func_5,
        6: func_6, 7: func_7, 8: func_8, 9: token_list,
        10: "window", 14: func_14, 15: func_15, 16: p,
        17: func_17, 18: func_18, 19: func_19, 20: func_20,
        21: func_21, 23: func_23, 24: func_24,
    })

    for token in token_list:
        try:
            fn = process_map.get(token[0])
            if callable(fn):
                fn(*token[1:])
        except Exception:
            continue
    return result or None


# ============ SSE 解析 ============
def iter_sse_payloads(response) -> Iterator[str]:
    for raw_line in response.iter_lines():
        if not raw_line:
            continue
        line = raw_line.decode("utf-8", errors="ignore") if isinstance(raw_line, bytes) else str(raw_line)
        if not line.startswith("data:"):
            continue
        payload = line[5:].strip()
        if payload:
            yield payload


# ============ 主流程 ============
def main():
    fp = FINGERPRINT
    ua = fp["user-agent"]
    impersonate = fp["impersonate"]

    # 构建 Session
    session = requests.Session(impersonate=impersonate, verify=True)
    session.headers.update({
        "User-Agent": ua,
        "Origin": BASE_URL,
        "Referer": BASE_URL + "/",
        "Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8,en-US;q=0.7",
        "Cache-Control": "no-cache",
        "Pragma": "no-cache",
        "Priority": "u=1, i",
        "Sec-Ch-Ua": fp["sec-ch-ua"],
        "Sec-Ch-Ua-Arch": '"x86"',
        "Sec-Ch-Ua-Bitness": '"64"',
        "Sec-Ch-Ua-Full-Version": '"143.0.3650.96"',
        "Sec-Ch-Ua-Full-Version-List": '"Microsoft Edge";v="143.0.3650.96", "Chromium";v="143.0.7499.147", "Not A(Brand";v="24.0.0.0"',
        "Sec-Ch-Ua-Mobile": fp["sec-ch-ua-mobile"],
        "Sec-Ch-Ua-Model": '""',
        "Sec-Ch-Ua-Platform": fp["sec-ch-ua-platform"],
        "Sec-Ch-Ua-Platform-Version": '"19.0.0"',
        "Sec-Fetch-Dest": "empty",
        "Sec-Fetch-Mode": "cors",
        "Sec-Fetch-Site": "same-origin",
        "OAI-Device-Id": DEVICE_ID,
        "OAI-Session-Id": SESSION_ID,
        "OAI-Language": "zh-CN",
        "OAI-Client-Version": CLIENT_VERSION,
        "OAI-Client-Build-Number": CLIENT_BUILD_NUMBER,
        "Authorization": f"Bearer {ACCESS_TOKEN}",
    })

    def api_headers(path: str, extra: dict | None = None) -> dict:
        h = dict(session.headers)
        h["X-OpenAI-Target-Path"] = path
        h["X-OpenAI-Target-Route"] = path
        if extra:
            h.update(extra)
        return h

    # ====== Step 1: Bootstrap (获取 PoW 脚本) ======
    print("=" * 60)
    print("Step 1: Bootstrap — GET /")
    bootstrap_headers = {
        "User-Agent": ua,
        "Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8",
        "Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8",
        "Sec-Ch-Ua": fp["sec-ch-ua"],
        "Sec-Ch-Ua-Mobile": fp["sec-ch-ua-mobile"],
        "Sec-Ch-Ua-Platform": fp["sec-ch-ua-platform"],
        "Sec-Fetch-Dest": "document",
        "Sec-Fetch-Mode": "navigate",
        "Sec-Fetch-Site": "none",
        "Sec-Fetch-User": "?1",
        "Upgrade-Insecure-Requests": "1",
    }
    r = session.get(BASE_URL + "/", headers=bootstrap_headers, timeout=30)
    ensure_ok(r, "bootstrap")
    pow_script_sources, pow_data_build = parse_pow_resources(r.text)
    if not pow_script_sources:
        pow_script_sources = [POW_SCRIPT_DEFAULT]
    print(f"  PoW scripts: {len(pow_script_sources)} found, data_build={pow_data_build[:50] if pow_data_build else 'N/A'}")

    # ====== Step 2: Chat Requirements (获取 sentinel token) ======
    print("\nStep 2: Chat Requirements — POST /backend-api/sentinel/chat-requirements")
    path = "/backend-api/sentinel/chat-requirements"
    legacy_token = build_legacy_requirements_token(ua, pow_script_sources, pow_data_build)
    r = session.post(
        BASE_URL + path,
        headers=api_headers(path, {"Content-Type": "application/json"}),
        json={"p": legacy_token},
        timeout=30,
    )
    ensure_ok(r, "chat-requirements")
    req_data = r.json()
    print(f"  Response keys: {list(req_data.keys())}")
    print(f"  Token: {req_data.get('token', 'N/A')[:50]}...")
    print(f"  Arkose required: {(req_data.get('arkose') or {}).get('required', False)}")

    # ====== Step 3: Solve PoW (如果需要) ======
    proof_token = ""
    pow_info = req_data.get("proofofwork") or {}
    if pow_info.get("required"):
        print("\nStep 3: Solving Proof of Work...")
        proof_token = build_proof_token(
            pow_info.get("seed", ""),
            pow_info.get("difficulty", ""),
            ua, pow_script_sources, pow_data_build,
        )
        print(f"  PoW solved: {proof_token[:50]}...")
    else:
        print("\nStep 3: PoW not required, skipping")

    # ====== Step 4: Solve Turnstile (如果需要) ======
    turnstile_token = ""
    turnstile_info = req_data.get("turnstile") or {}
    if turnstile_info.get("required") and turnstile_info.get("dx"):
        print("\nStep 4: Solving Turnstile...")
        turnstile_token = solve_turnstile_token(turnstile_info["dx"], legacy_token) or ""
        print(f"  Turnstile solved: {'YES' if turnstile_token else 'FAILED'}")
    else:
        print("\nStep 4: Turnstile not required, skipping")

    # 构建 requirements
    req_token = req_data.get("token", "")
    so_token = req_data.get("so_token", "")
    if not req_token:
        raise RuntimeError(f"Missing chat requirements token: {req_data}")

    # ====== Step 5: Verify /me ======
    print("\nStep 5: Verify Account — GET /backend-api/me")
    r = session.get(BASE_URL + "/backend-api/me", headers=api_headers("/backend-api/me"), timeout=20)
    ensure_ok(r, "me")
    me = r.json()
    print(f"  Email: {me.get('email')}, Plan: {me.get('name')}")

    # ====== Step 6: Prepare Image Conversation ======
    print(f"\nStep 6: Prepare Image — POST /backend-api/f/conversation/prepare")
    prepare_path = "/backend-api/f/conversation/prepare"
    prepare_headers = {
        "Content-Type": "application/json",
        "Accept": "*/*",
        "OpenAI-Sentinel-Chat-Requirements-Token": req_token,
    }
    if proof_token:
        prepare_headers["OpenAI-Sentinel-Proof-Token"] = proof_token
    prepare_payload = {
        "action": "next",
        "fork_from_shared_post": False,
        "parent_message_id": new_uuid(),
        "model": "gpt-5-5",  # gpt-image-2 对应的底层 model slug
        "client_prepare_state": "success",
        "timezone_offset_min": -480,
        "timezone": "Asia/Shanghai",
        "conversation_mode": {"kind": "primary_assistant"},
        "system_hints": ["picture_v2"],
        "partial_query": {
            "id": new_uuid(),
            "author": {"role": "user"},
            "content": {"content_type": "text", "parts": [PROMPT]},
        },
        "supports_buffering": True,
        "supported_encodings": ["v1"],
        "client_contextual_info": {"app_name": "chatgpt.com"},
    }
    r = session.post(
        BASE_URL + prepare_path,
        headers=api_headers(prepare_path, prepare_headers),
        json=prepare_payload,
        timeout=60,
    )
    ensure_ok(r, "prepare")
    prepare_resp = r.json()
    conduit_token = prepare_resp.get("conduit_token", "")
    print(f"  conduit_token: {conduit_token[:50]}..." if conduit_token else "  ERROR: No conduit_token!")
    print(f"  Full prepare response: {json.dumps(prepare_resp, indent=2, ensure_ascii=False)[:500]}")

    # ====== Step 7: Start Image Generation (SSE) ======
    print(f"\nStep 7: Generate Image — POST /backend-api/f/conversation (SSE)")
    gen_path = "/backend-api/f/conversation"
    gen_headers = {
        "Content-Type": "application/json",
        "Accept": "text/event-stream",
        "OpenAI-Sentinel-Chat-Requirements-Token": req_token,
        "X-Conduit-Token": conduit_token,
        "X-Oai-Turn-Trace-Id": new_uuid(),
    }
    if proof_token:
        gen_headers["OpenAI-Sentinel-Proof-Token"] = proof_token

    gen_payload = {
        "action": "next",
        "messages": [{
            "id": new_uuid(),
            "author": {"role": "user"},
            "create_time": time.time(),
            "content": {"content_type": "text", "parts": [PROMPT]},
            "metadata": {
                "developer_mode_connector_ids": [],
                "selected_github_repos": [],
                "selected_all_github_repos": False,
                "system_hints": ["picture_v2"],
                "serialization_metadata": {"custom_symbol_offsets": []},
            },
        }],
        "parent_message_id": new_uuid(),
        "model": "gpt-5-5",
        "client_prepare_state": "sent",
        "timezone_offset_min": -480,
        "timezone": "Asia/Shanghai",
        "conversation_mode": {"kind": "primary_assistant"},
        "enable_message_followups": True,
        "system_hints": ["picture_v2"],
        "supports_buffering": True,
        "supported_encodings": ["v1"],
        "client_contextual_info": {
            "is_dark_mode": False,
            "time_since_loaded": 1200,
            "page_height": 1072,
            "page_width": 1724,
            "pixel_ratio": 1.2,
            "screen_height": 1440,
            "screen_width": 2560,
            "app_name": "chatgpt.com",
        },
        "paragen_cot_summary_display_override": "allow",
        "force_parallel_switch": "auto",
    }

    r = session.post(
        BASE_URL + gen_path,
        headers=api_headers(gen_path, gen_headers),
        json=gen_payload,
        timeout=300,
        stream=True,
    )
    ensure_ok(r, "image_generation")

    print(f"  Status: {r.status_code}")
    print(f"  Content-Type: {r.headers.get('content-type', 'N/A')}")

    # 收集 SSE 事件
    all_events = []
    conversation_id = None
    for payload in iter_sse_payloads(r):
        try:
            data = json.loads(payload)
        except json.JSONDecodeError:
            all_events.append({"raw": payload})
            print(f"  SSE(raw): {payload[:200]}")
            continue

        # data 可能是 dict, list, 或其他类型
        if not isinstance(data, dict):
            all_events.append({"parsed": data})
            print(f"  SSE(parsed): type={type(data).__name__}, value={str(data)[:200]}")
            continue

        all_events.append(data)

        # 提取 conversation_id
        if not conversation_id:
            cid = data.get("conversation_id")
            if cid:
                conversation_id = cid
                print(f"  conversation_id: {conversation_id}")

        # 提取 message_id
        msg_id = data.get("message_id", "")
        if msg_id:
            print(f"  SSE: message_id={msg_id[:40]}")

        # 打印关键事件摘要
        msg = data.get("message") or {}
        if msg:
            content = msg.get("content") or {}
            ct = content.get("content_type", "")
            ap = content.get("asset_pointer", "")
            author = msg.get("author") or {}
            role = author.get("role", "")
            msg_type = msg.get("metadata", {}).get("message_type", "")
            if ct:
                print(f"  SSE: content_type={ct}, asset_pointer={ap[:60] if ap else 'N/A'}")
            elif role == "tool":
                print(f"  SSE: tool message, message_type={msg_type}")
            else:
                print(f"  SSE: message role={role}, content_type={ct or 'N/A'}")

        # 检查是否有 error
        if data.get("error"):
            print(f"  SSE ERROR: {data.get('error')}")

    r.close()

    print(f"\n  Total SSE events: {len(all_events)}")

    # 保存完整 SSE 响应
    sse_file = OUTPUT_DIR / "image-gen-sse-response.json"
    with open(sse_file, "w", encoding="utf-8") as f:
        json.dump(all_events, f, indent=2, ensure_ascii=False)
    print(f"  SSE response saved to: {sse_file}")

    # ====== Step 8: 从 SSE events 直接提取 image_asset_pointer ======
    print("\nStep 8: Extracting image references from SSE events...")
    file_ids = []
    sediment_ids = []
    file_pat = re.compile(r"file-service://([A-Za-z0-9_-]+)")
    sed_pat = re.compile(r"sediment://([A-Za-z0-9_]+)")

    for event in all_events:
        if not isinstance(event, dict):
            continue
        # SSE events 有两种结构: {"v": {"message": ...}} 和 {"type": "..."}
        for source in [event, event.get("v", {})]:
            if not isinstance(source, dict):
                continue
            msg = source.get("message") or {}
            content = msg.get("content") or {}
            parts = content.get("parts") or []
            if not isinstance(parts, list):
                continue
            for part in parts:
                if not isinstance(part, dict):
                    continue
                if part.get("content_type") == "image_asset_pointer":
                    ap = part.get("asset_pointer", "")
                    for hit in file_pat.findall(ap):
                        if hit not in file_ids and hit != "file_upload":
                            file_ids.append(hit)
                    for hit in sed_pat.findall(ap):
                        if hit not in sediment_ids:
                            sediment_ids.append(hit)
                    # 打印图片元数据
                    meta = part.get("metadata", {})
                    gen = meta.get("generation", {})
                    print(f"  Image: {part.get('width')}x{part.get('height')}, "
                          f"{part.get('size_bytes')} bytes, "
                          f"gen_id={gen.get('gen_id', '?')[:20]}...")

    # 也尝试从 raw text 中搜索
    full_text = json.dumps(all_events)
    for hit in sed_pat.findall(full_text):
        if hit not in sediment_ids:
            sediment_ids.append(hit)
    for hit in file_pat.findall(full_text):
        if hit not in file_ids and hit != "file_upload":
            file_ids.append(hit)

    print(f"  file_ids: {file_ids}")
    print(f"  sediment_ids: {sediment_ids}")

    # ====== Step 9: 如果 SSE 中没拿到，轮询 conversation ======
    if not file_ids and not sediment_ids and conversation_id:
        print(f"\nStep 9: Polling conversation {conversation_id} for results...")
        start = time.time()
        while time.time() - start < 60:
            time.sleep(3)
            r = session.get(
                BASE_URL + f"/backend-api/conversation/{conversation_id}",
                headers=api_headers(f"/backend-api/conversation/{conversation_id}", {"Accept": "application/json"}),
                timeout=30,
            )
            if r.status_code != 200:
                continue
            conv = r.json()
            mapping = conv.get("mapping") or {}
            for msg_id, node in mapping.items():
                msg = (node or {}).get("message") or {}
                if (msg.get("author") or {}).get("role") != "tool":
                    continue
                for part in (msg.get("content") or {}).get("parts") or []:
                    if not isinstance(part, dict):
                        continue
                    ap = part.get("asset_pointer", "")
                    for hit in sed_pat.findall(ap):
                        if hit not in sediment_ids:
                            sediment_ids.append(hit)
                    for hit in file_pat.findall(ap):
                        if hit not in file_ids and hit != "file_upload":
                            file_ids.append(hit)
            if file_ids or sediment_ids:
                print(f"  Got results after {time.time() - start:.1f}s!")
                break
            print(f"  Polling... {time.time() - start:.0f}s")

    # ====== Step 10: 下载图片 ======
    if file_ids or sediment_ids:
        print(f"\nStep 10: Downloading images...")
        downloaded = 0

        for fid in file_ids:
            download_path = f"/backend-api/files/{fid}/download"
            try:
                r = session.get(
                    BASE_URL + download_path,
                    headers=api_headers(download_path, {"Accept": "application/json"}),
                    timeout=60,
                )
                ensure_ok(r, f"download_{fid}")
                url = r.json().get("download_url") or r.json().get("url") or ""
                if url:
                    print(f"  Downloading file {fid[:40]}...")
                    img_r = session.get(url, timeout=120)
                    ensure_ok(img_r, f"image_download_{fid}")
                    img_path = OUTPUT_DIR / f"{fid}.png"
                    img_path.write_bytes(img_r.content)
                    print(f"  Saved: {img_path} ({len(img_r.content)} bytes)")
                    downloaded += 1
            except Exception as e:
                print(f"  File download failed for {fid[:40]}: {e}")

        for sid in sediment_ids:
            att_path = f"/backend-api/conversation/{conversation_id}/attachment/{sid}/download"
            try:
                r = session.get(
                    BASE_URL + att_path,
                    headers=api_headers(att_path, {"Accept": "application/json"}),
                    timeout=60,
                )
                if r.status_code == 200:
                    url = r.json().get("download_url") or r.json().get("url") or ""
                    if url:
                        print(f"  Downloading sediment {sid[:40]}...")
                        img_r = session.get(url, timeout=120)
                        img_path = OUTPUT_DIR / f"{sid}.png"
                        img_path.write_bytes(img_r.content)
                        print(f"  Saved: {img_path} ({len(img_r.content)} bytes)")
                        downloaded += 1
            except Exception as e:
                print(f"  Sediment download failed for {sid[:40]}: {e}")

        print(f"  Total downloaded: {downloaded} images")
    else:
        print("\nNo image references found in SSE response.")

    print("\n" + "=" * 60)
    print("Complete!")


if __name__ == "__main__":
    main()
