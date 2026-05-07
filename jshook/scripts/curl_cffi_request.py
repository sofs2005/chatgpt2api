"""
ChatGPT gpt-image-2 生图请求脚本
使用 curl-cffi 模拟 Chrome TLS 指纹绕过 Cloudflare WAF
"""
import json
import uuid
import sys
from curl_cffi import requests

# ============ 配置 ============
ACCESS_TOKEN = "YOUR_ACCESS_TOKEN_HERE"

PROMPT = "A cute cat wearing a wizard hat, digital illustration"

OUTPUT_DIR = "jshook/responses"

# ============ 请求体构建 ============
def build_request_body(prompt: str) -> dict:
    return {
        "action": "next",
        "model": "auto",
        "messages": [
            {
                "id": str(uuid.uuid4()),
                "author": {"role": "user"},
                "content": {
                    "content_type": "multimodal_text",
                    "parts": [prompt]
                }
            }
        ],
        "timezone": "Asia/Shanghai",
        "history_and_training_disabled": False,
        "force_paragen": False,
        "enable_paragen": False,
        "callsite_id": "request_completion.tool_landing_pages.image_gen.images_app.images_app_composer.2",
        "image_generation_mode": "image",
    }


# ============ SSE 解析 ============
def parse_sse_stream(response):
    """解析 SSE 流式响应，收集所有事件"""
    events = []
    current_event = {}

    for line in response.iter_lines():
        if not line:
            continue

        decoded = line.decode("utf-8", errors="replace")

        if decoded.startswith("data: "):
            data_str = decoded[6:]
            if data_str == "[DONE]":
                events.append({"type": "done"})
                break

            try:
                data = json.loads(data_str)
                events.append(data)
            except json.JSONDecodeError:
                events.append({"type": "raw", "data": data_str})

        elif decoded.startswith("event: "):
            current_event["event_type"] = decoded[7:]

        elif decoded.startswith(": "):
            pass  # SSE comment/heartbeat

    return events


# ============ 主流程 ============
def main():
    url = "https://chatgpt.com/backend-api/conversation"
    headers = {
        "Authorization": f"Bearer {ACCESS_TOKEN}",
        "Content-Type": "application/json",
        "Accept": "text/event-stream",
        "Origin": "https://chatgpt.com",
        "Referer": "https://chatgpt.com/images",
        "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
        "Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8",
        "Accept-Encoding": "gzip, deflate, br",
        "Cache-Control": "no-cache",
        "Pragma": "no-cache",
        "Sec-Ch-Ua": '"Chromium";v="131", "Not_A Brand";v="24"',
        "Sec-Ch-Ua-Mobile": "?0",
        "Sec-Ch-Ua-Platform": '"Windows"',
        "Sec-Fetch-Dest": "empty",
        "Sec-Fetch-Mode": "cors",
        "Sec-Fetch-Site": "same-origin",
        "X-Conversation-Id": str(uuid.uuid4()),
        "X-Requested-With": "XMLHttpRequest",
    }

    body = build_request_body(PROMPT)

    print("=" * 60)
    print(f"Prompt: {PROMPT}")
    print(f"URL: {url}")
    print(f"Request body keys: {list(body.keys())}")
    print("=" * 60)

    # 尝试多种 impersonate 配置
    impersonate_configs = [
        "chrome131",
        "chrome124",
        "chrome120",
        "chrome110",
        "chrome107",
        "chrome99",
        "safari17_0",
        "edge101",
        "firefox117",
    ]

    for impersonate in impersonate_configs:
        print(f"\n--- Trying impersonate: {impersonate} ---")

        try:
            response = requests.post(
                url,
                headers=headers,
                json=body,
                impersonate=impersonate,
                timeout=120,
                stream=True,
            )

            print(f"Status: {response.status_code}")
            print(f"Content-Type: {response.headers.get('content-type', 'N/A')}")

            # 检查是否命中 Cloudflare
            if response.status_code == 403:
                try:
                    error = response.json()
                    print(f"403 Error: {json.dumps(error, indent=2, ensure_ascii=False)}")
                except Exception:
                    print(f"403 Raw: {response.text[:500]}")
                continue

            # 检查 Cloudflare Challenge (HTML)
            ct = response.headers.get("content-type", "")
            if "text/html" in ct and "_cf_chl" in response.text[:500]:
                print("Got Cloudflare Challenge page, trying next impersonate...")
                continue

            if response.status_code == 200:
                print("SUCCESS! Got 200 with SSE stream")

                events = parse_sse_stream(response)
                print(f"Total SSE events: {len(events)}")

                # 保存完整响应
                with open(f"{OUTPUT_DIR}/sse-response-{impersonate}.json", "w", encoding="utf-8") as f:
                    json.dump(events, f, indent=2, ensure_ascii=False)

                # 打印事件摘要
                for i, evt in enumerate(events):
                    if isinstance(evt, dict):
                        evt_type = evt.get("type", evt.get("message", {}).get("content", {}).get("content_type", "?"))
                        msg_id = evt.get("message_id", "")
                        print(f"  [{i}] type={evt_type}, msg_id={msg_id[:40]}...")

                # 提取 asset_pointer
                asset_pointers = []
                for evt in events:
                    if isinstance(evt, dict):
                        msg = evt.get("message", {})
                        content = msg.get("content", {})
                        if content.get("content_type") == "t2uay3k":
                            asset_ptr = content.get("asset_pointer")
                            if asset_ptr:
                                asset_pointers.append(asset_ptr)

                if asset_pointers:
                    print(f"\nAsset Pointers found: {asset_pointers}")

                return events

            elif response.status_code == 422:
                error = response.json()
                print(f"422 Validation Error: {json.dumps(error, indent=2, ensure_ascii=False)[:1000]}")
                # 422 说明绕过了 Cloudflare，到达了后端
                break

            else:
                body_snippet = response.text[:500]
                print(f"Unexpected {response.status_code}: {body_snippet}")

        except Exception as e:
            print(f"Error with {impersonate}: {e}")

    print("\nAll impersonate attempts failed.")


if __name__ == "__main__":
    main()
