"""
验证 /backend-api/conversation (文本聊天) 端点
与 /backend-api/f/conversation (生图) 端点的差异
"""
import json
import sys
import time
import uuid
from pathlib import Path

import pybase64
import hashlib
import random
from datetime import datetime, timedelta, timezone

sys.path.insert(0, str(Path(__file__).resolve().parent))
from image_gen_full_flow import (
    ACCESS_TOKEN, DEVICE_ID, SESSION_ID, CLIENT_VERSION, CLIENT_BUILD_NUMBER,
    FINGERPRINT, new_uuid, ensure_ok, build_proof_token,
    build_legacy_requirements_token, parse_pow_resources, iter_sse_payloads,
)
from curl_cffi import requests

BASE_URL = "https://chatgpt.com"
POW_SCRIPT_DEFAULT = "https://chatgpt.com/backend-api/sentinel/sdk.js"

def test_text_chat_endpoint():
    """测试 /backend-api/conversation (文本聊天) 端点"""
    fp = FINGERPRINT
    ua = fp["user-agent"]
    impersonate = fp["impersonate"]

    session = requests.Session(impersonate=impersonate, verify=True)
    session.headers.update({
        "User-Agent": ua,
        "Origin": BASE_URL,
        "Referer": BASE_URL + "/",
        "Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8",
        "Cache-Control": "no-cache",
        "Pragma": "no-cache",
        "Sec-Ch-Ua": fp["sec-ch-ua"],
        "Sec-Ch-Ua-Mobile": fp["sec-ch-ua-mobile"],
        "Sec-Ch-Ua-Platform": fp["sec-ch-ua-platform"],
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

    def api_headers(path, extra=None):
        h = dict(session.headers)
        h["X-OpenAI-Target-Path"] = path
        h["X-OpenAI-Target-Route"] = path
        if extra:
            h.update(extra)
        return h

    # Bootstrap
    print("Step 1: Bootstrap...")
    r = session.get(BASE_URL + "/", headers={
        "User-Agent": ua,
        "Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
        "Sec-Fetch-Dest": "document",
        "Sec-Fetch-Mode": "navigate",
        "Sec-Fetch-Site": "none",
    }, timeout=30)
    ensure_ok(r, "bootstrap")
    sources, data_build = parse_pow_resources(r.text)
    if not sources:
        sources = [POW_SCRIPT_DEFAULT]

    # Chat Requirements
    print("Step 2: Chat Requirements...")
    path = "/backend-api/sentinel/chat-requirements"
    legacy = build_legacy_requirements_token(ua, sources, data_build)
    r = session.post(BASE_URL + path,
        headers=api_headers(path, {"Content-Type": "application/json"}),
        json={"p": legacy}, timeout=30)
    ensure_ok(r, "chat-requirements")
    req_data = r.json()
    req_token = req_data.get("token", "")
    pow_info = req_data.get("proofofwork") or {}

    # PoW
    proof_token = ""
    if pow_info.get("required"):
        proof_token = build_proof_token(
            pow_info["seed"], pow_info["difficulty"], ua, sources, data_build)

    # ============ 测试 1: /backend-api/conversation (文本聊天) ============
    print("\n===== TEST 1: /backend-api/conversation (text chat) =====")
    test_path = "/backend-api/conversation"
    test_headers = {
        "Content-Type": "application/json",
        "Accept": "text/event-stream",
        "OpenAI-Sentinel-Chat-Requirements-Token": req_token,
        "X-Oai-Turn-Trace-Id": new_uuid(),
    }
    if proof_token:
        test_headers["OpenAI-Sentinel-Proof-Token"] = proof_token

    # 纯文本请求 — content_type: "text"
    text_body = {
        "action": "next",
        "messages": [{
            "id": new_uuid(),
            "author": {"role": "user"},
            "create_time": time.time(),
            "content": {"content_type": "text", "parts": ["Hello, just say 'hi' back"]},
        }],
        "model": "auto",
        "timezone": "Asia/Shanghai",
        "timezone_offset_min": -480,
        "history_and_training_disabled": False,
    }

    print(f"  Request: {json.dumps(text_body, indent=2, ensure_ascii=False)[:500]}")
    r = session.post(
        BASE_URL + test_path,
        headers=api_headers(test_path, test_headers),
        json=text_body,
        timeout=60,
        stream=True,
    )
    print(f"  Status: {r.status_code}")
    print(f"  Content-Type: {r.headers.get('content-type', 'N/A')}")

    events = []
    if r.status_code == 200:
        for payload in iter_sse_payloads(r):
            if payload == "[DONE]":
                events.append({"type": "done"})
                break
            try:
                data = json.loads(payload)
            except json.JSONDecodeError:
                events.append({"raw": payload})
                continue
            events.append(data if isinstance(data, dict) else {"parsed": data})

        print(f"  SSE events: {len(events)}")
        # 分类统计
        event_types = {}
        for e in events:
            t = e.get("type", "patch" if "o" in e else "other")
            event_types[t] = event_types.get(t, 0) + 1
        print(f"  Event type distribution: {event_types}")

        # 保存
        out = Path("jshook/responses/text-chat-sse-response.json")
        out.parent.mkdir(parents=True, exist_ok=True)
        out.write_text(json.dumps(events, indent=2, ensure_ascii=False), encoding="utf-8")
        print(f"  Saved to: {out}")

        r.close()
    else:
        try:
            error = r.json()
            print(f"  Error: {json.dumps(error, indent=2, ensure_ascii=False)[:500]}")
        except Exception:
            print(f"  Raw: {r.text[:500]}")

    # ============ 测试 2: /backend-api/conversation 带 multimodal_text ============
    print("\n===== TEST 2: /backend-api/conversation with multimodal_text =====")
    mm_body = {
        "action": "next",
        "messages": [{
            "id": new_uuid(),
            "author": {"role": "user"},
            "create_time": time.time(),
            "content": {"content_type": "multimodal_text", "parts": ["Hello, just say 'hi' back"]},
        }],
        "model": "auto",
        "timezone": "Asia/Shanghai",
        "timezone_offset_min": -480,
    }

    print(f"  Request: {json.dumps(mm_body, indent=2, ensure_ascii=False)[:500]}")
    r = session.post(
        BASE_URL + test_path,
        headers=api_headers(test_path, test_headers),
        json=mm_body,
        timeout=60,
        stream=True,
    )
    print(f"  Status: {r.status_code}")
    print(f"  Content-Type: {r.headers.get('content-type', 'N/A')}")

    if r.status_code == 200:
        count = 0
        for payload in iter_sse_payloads(r):
            if payload == "[DONE]":
                break
            count += 1
        print(f"  SSE events: {count}")
        r.close()
    else:
        try:
            error = r.json()
            print(f"  Error: {json.dumps(error, indent=2, ensure_ascii=False)[:500]}")
        except Exception:
            print(f"  Raw: {r.text[:500]}")

    # ============ 测试 3: /backend-api/conversation 带 conversation_id ============
    print("\n===== TEST 3: /backend-api/conversation with conversation_id =====")
    cid_body = {
        "action": "next",
        "conversation_id": new_uuid(),
        "messages": [{
            "id": new_uuid(),
            "author": {"role": "user"},
            "create_time": time.time(),
            "content": {"content_type": "text", "parts": ["Hello"]},
        }],
        "model": "auto",
        "timezone": "Asia/Shanghai",
        "timezone_offset_min": -480,
    }

    r = session.post(
        BASE_URL + test_path,
        headers=api_headers(test_path, test_headers),
        json=cid_body,
        timeout=60,
        stream=True,
    )
    print(f"  Status: {r.status_code}")
    if r.status_code != 200:
        try:
            error = r.json()
            print(f"  Error: {json.dumps(error, indent=2, ensure_ascii=False)[:500]}")
        except Exception:
            print(f"  Raw: {r.text[:500]}")
    else:
        count = 0
        for payload in iter_sse_payloads(r):
            if payload == "[DONE]":
                break
            count += 1
        print(f"  SSE events: {count}")
        r.close()

    print("\n===== All tests complete! =====")


if __name__ == "__main__":
    test_text_chat_endpoint()
