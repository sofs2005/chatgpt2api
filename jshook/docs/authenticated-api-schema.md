# ChatGPT 认证生图 API Schema（实抓验证）

> 更新: 2026-05-07 — 已通过 curl-cffi + PoW 完整绕过 Cloudflare，成功触发生图并下载图片

## 一、Cloudflare 绕过方案（✅ 已验证）

### 成功组合

| 组件 | 配置 |
|---|---|
| **HTTP 库** | `curl-cffi` 0.15.0 |
| **TLS 指纹** | `edge101` (Microsoft Edge 143) |
| **PoW** | SHA3-512 Proof of Work（`gAAAAAB` 前缀 token） |
| **Chat Requirements** | `POST /backend-api/sentinel/chat-requirements` |
| **Turnstile** | 当前未触发（arkose.required = false） |

### 认证

```http
Authorization: Bearer <oauth_access_token>
```

- OAuth access_token（JWT，aud=`https://api.openai.com/v1`）即可
- 无需 ChatGPT Web Session Cookie
- 无需 Arkose Token（当前 sentinel 未要求）

### 必需 HTTP Headers

```http
X-OpenAI-Target-Path: <path>
X-OpenAI-Target-Route: <path>
OAI-Device-Id: <random_uuid>
OAI-Session-Id: <random_uuid>
OAI-Language: zh-CN
OAI-Client-Version: prod-be885abbfcfe7b1f511e88b3003d9ee44757fbad
OAI-Client-Build-Number: 5955942
OpenAI-Sentinel-Chat-Requirements-Token: <sentinel_token>
OpenAI-Sentinel-Proof-Token: gAAAAAB<base64_pow_solution>
```

## 二、完整生图链路（6 步）

### Step 1: Bootstrap — `GET /`

```python
session.get("https://chatgpt.com/", headers={
    "Accept": "text/html,...",
    "Sec-Fetch-Dest": "document",
    "Sec-Fetch-Mode": "navigate",
})
```

提取 HTML 中的 `<script src="...">` 用于 PoW 配置生成。

### Step 2: Chat Requirements — `POST /backend-api/sentinel/chat-requirements`

```json
// Request
{
  "p": "gAAAAAC<legacy_requirements_token>"
}

// Response
{
  "persona": "..." | null,
  "token": "gAAAAAB...",          // ← 后续请求需要的 sentinel token
  "expire_after": 3600,
  "expire_at": 1778125774,
  "turnstile": { "required": false },
  "proofofwork": {
    "required": true,
    "seed": "...",
    "difficulty": "000fffff..."
  },
  "so": { ... }
}
```

### Step 3: Solve PoW（PoW 求解）

```python
# 使用 SHA3-512，难度匹配
# config 从 bootstrap 的 HTML 中提取（scripts, data-build）
# 输出: "gAAAAAB" + base64(json([...]))
proof_token = build_proof_token(seed, difficulty, user_agent, scripts, data_build)
```

### Step 4: Prepare Image — `POST /backend-api/f/conversation/prepare`

```json
// Request
{
  "action": "next",
  "parent_message_id": "<uuid>",
  "model": "gpt-5-5",            // gpt-image-2 → gpt-5-5
  "client_prepare_state": "success",
  "timezone_offset_min": -480,
  "timezone": "Asia/Shanghai",
  "conversation_mode": { "kind": "primary_assistant" },
  "system_hints": ["picture_v2"],
  "partial_query": {
    "id": "<uuid>",
    "author": { "role": "user" },
    "content": {
      "content_type": "text",     // 纯文本用 "text"，不是 "multimodal_text"
      "parts": ["prompt text"]
    }
  },
  "supports_buffering": true,
  "supported_encodings": ["v1"],
  "client_contextual_info": { "app_name": "chatgpt.com" }
}

// Response
{
  "status": "ok",
  "conduit_token": "eyJhbGciOiJFUzI1NiIs..."   // ← 生图时需要
}
```

### Step 5: Generate Image — `POST /backend-api/f/conversation` (SSE)

```json
// Request — 注意此端点是 /f/conversation 不是 /conversation
{
  "action": "next",
  "messages": [{
    "id": "<uuid>",
    "author": { "role": "user" },
    "create_time": 1778122176.0,
    "content": {
      "content_type": "text",
      "parts": ["A cute cat wearing a wizard hat, digital illustration"]
    },
    "metadata": {
      "system_hints": ["picture_v2"],
      "serialization_metadata": { "custom_symbol_offsets": [] }
    }
  }],
  "parent_message_id": "<uuid>",
  "model": "gpt-5-5",
  "client_prepare_state": "sent",
  "timezone_offset_min": -480,
  "timezone": "Asia/Shanghai",
  "conversation_mode": { "kind": "primary_assistant" },
  "system_hints": ["picture_v2"],
  "supports_buffering": true,
  "supported_encodings": ["v1"],
  "client_contextual_info": {
    "is_dark_mode": false,
    "page_height": 1072, "page_width": 1724,
    "pixel_ratio": 1.2,
    "screen_height": 1440, "screen_width": 2560,
    "app_name": "chatgpt.com"
  },
  "force_parallel_switch": "auto"
}

// 必需 Headers（除了基础 headers 外）
"Accept": "text/event-stream",
"OpenAI-Sentinel-Chat-Requirements-Token": "<sentinel_token>",
"OpenAI-Sentinel-Proof-Token": "gAAAAAB<proof>",
"X-Conduit-Token": "<conduit_token>",
"X-Oai-Turn-Trace-Id": "<uuid>"
```

### Step 6: Download Image

从 SSE 响应中提取 `asset_pointer`，通过 attachment 端点下载：

```python
# SSE 事件中的 image_asset_pointer
{
  "content_type": "image_asset_pointer",
  "asset_pointer": "sediment://file_000000004e00720baf5d5b3babb57feb",
  "size_bytes": 2504425,
  "width": 1254,
  "height": 1254,
  "metadata": {
    "generation": {
      "gen_id": "0c6d64e1-33d4-49b3-b6fe-b8262720fc3f",
      "gen_size": "smimage",
      "gen_size_v2": "16",
      "height": 1254, "width": 1254,
      "orientation": "square"
    }
  }
}

# 下载方法 1: Attachment Download（推荐）
GET /backend-api/conversation/{conversation_id}/attachment/{sediment_id}/download
→ {"download_url": "https://chatgpt.com/backend-api/estuary/content?id=..."}
→ GET {download_url}

# 下载方法 2: File Download
GET /backend-api/files/{file_id}/download
→ {"download_url": "..."}
→ GET {download_url}
```

## 三、SSE 响应结构（实抓）

### 响应 Content-Type

```
Content-Type: text/event-stream; charset=utf-8
```

### 3.1 生图端点 SSE (`/backend-api/f/conversation`)

#### 事件类型（按顺序）

| # | 格式 | 说明 |
|---|---|---|
| 1 | `"v1"` (纯字符串) | 编码版本 |
| 2 | `type: "resume_conversation_token"` | 含 `conversation_id`、resume token |
| 3 | Patch: `content_type: "model_editable_context"` | 模型上下文初始化 |
| 4 | Patch: `content_type: "code", recipient: "t2uay3k.sj1i4kz"` | 图片工具调用（`{"skipped_mainline":true}`） |
| 5 | Replace: `v: "finished_successfully"` | 状态更新 |
| 6 | `type: "title_generation"` | 对话标题 |
| 7 | `type: "message_marker"` | `user_visible_token` 标记 |
| 8 | Patch: tool 消息, `content_type: "multimodal_text"` | **🔴 核心：图片资产指针** |
| 9 | Patch: tool 消息, `content_type: "text"` | 图片生成后的追问指令 |
| 10 | `type: "message_marker"` | `final_channel_token` 标记 |
| 11 | Patch: assistant 消息, `content_type: "text"` | AI 回复文本 |
| 12 | `type: "message_marker"` | `last_token` 标记 |
| 13 | `type: "title_generation"` | 最终标题 |
| 14 | `type: "server_ste_metadata"` | 遥测元数据 |
| 15 | `type: "message_stream_complete"` | 流结束 |
| 16 | `[DONE]` | SSE 终止符 |

#### SSE 消息结构（3 种格式）

**格式 A — Patch 事件（消息包装在 `v` 内）**
```json
{
  "p": "",              // JSON Pointer path（空 = 根）
  "o": "add",           // 操作: add | replace
  "v": {                // 值 — message + conversation_id 都在这里
    "message": {
      "id": "...",
      "author": { "role": "tool", "name": "t2uay3k.sj1i4kz" },
      "content": {
        "content_type": "multimodal_text",
        "parts": [{ "content_type": "image_asset_pointer", ... }]
      }
    },
    "conversation_id": "...",
    "error": null
  },
  "c": 2                // 计数器
}
```

**格式 B — 类型事件**
```json
{
  "type": "title_generation" | "message_marker" | "server_ste_metadata" | "message_stream_complete" | "resume_conversation_token",
  "conversation_id": "..."
}
```

**格式 C — 简写替换**
```json
{
  "p": "/message/status",
  "o": "replace",
  "v": "finished_successfully"
}
```

### 3.2 文本聊天端点 SSE (`/backend-api/conversation`)

格式不同 — **消息直接在顶层**，无 patch 包装:

```json
{
  "message": {
    "id": "...",
    "author": { "role": "assistant" },
    "content": { "content_type": "text", "parts": ["回复内容"] },
    "status": "finished_successfully",
    "end_turn": true
  },
  "conversation_id": "...",
  "error": null
}
```

类型事件（`title_generation`, `message_marker`, `message_stream_complete` 等）与生图端点格式相同。

### 3.3 两个端点的关键区别

| | 生图 (`/f/conversation`) | 文本聊天 (`/conversation`) |
|---|---|---|
| 消息格式 | Patch 包装 `{"p":"","o":"add","v":{...}}` | 直接 `{"message":{...}}` |
| 首个事件 | `"v1"` 字符串 | `resume_conversation_token` |
| 需要 prepare | 是 | 否 |
| 需要 conduit_token | 是 | 否 |
| tool 消息 | 有 (t2uay3k) | 无 |
| `turn_use_case` | `"image gen"` | `"text"` |
| 运行时模型 | `i-5-mini-m` | `i-5-mini-m` |
| 底层模型 | `gpt-5-5` | `gpt-5-5` |

### 图片资产指针详细结构

```json
{
  "content_type": "image_asset_pointer",
  "asset_pointer": "sediment://file_000000004e00720baf5d5b3babb57feb",
  "size_bytes": 2504425,
  "width": 1254,
  "height": 1254,
  "fovea": null,
  "metadata": {
    "dalle": {
      "gen_id": "0c6d64e1-33d4-49b3-b6fe-b8262720fc3f",
      "prompt": "",
      "seed": null,
      "parent_gen_id": null,
      "edit_op": null,
      "serialization_title": "DALL-E generation metadata"
    },
    "generation": {
      "gen_id": "0c6d64e1-33d4-49b3-b6fe-b8262720fc3f",
      "gen_size": "smimage",
      "gen_size_v2": "16",
      "seed": null,
      "parent_gen_id": null,
      "height": 1254,
      "width": 1254,
      "transparent_background": false,
      "orientation": "square"
    },
    "container_pixel_height": 1254,
    "container_pixel_width": 1254,
    "sanitized": false,
    "watermarked_asset_pointer": null,
    "is_no_auth_placeholder": null
  }
}
```

## 四、模型路由

| 用户请求 | 底层 model slug |
|---|---|
| `gpt-image-2` / `auto` | `gpt-5-5` |
| `codex-gpt-image-2` | `codex-gpt-image-2` |

实际生图使用的模型（从 server_ste_metadata 确认）：
- `model_slug`: `i-5-mini-m`（图片理解模型，用于生成回复文本）
- `turn_use_case`: `image gen`
- `plan_type`: `plus`

## 五、资产生命周期

```
1. Prepare:  POST /backend-api/f/conversation/prepare → conduit_token
2. Generate: POST /backend-api/f/conversation (SSE) → conversation_id + sediment:// asset_pointer
3. Download: GET /backend-api/conversation/{cid}/attachment/{sid}/download → download_url
4. Fetch:    GET {download_url} (estuary CDN) → 图片二进制
```

## 六、已验证端点完整列表

| 端点 | 方法 | 认证 | 状态 |
|---|---|---|---|
| `GET /` | GET | 无 | ✅ bootstrap + PoW 脚本提取 |
| `POST /backend-api/sentinel/chat-requirements` | POST | Bearer + PoW | ✅ 获取 sentinel token |
| `GET /backend-api/me` | GET | Bearer | ✅ 200 + 用户信息 |
| `POST /backend-api/f/conversation/prepare` | POST | Bearer + Sentinel | ✅ 获取 conduit_token |
| `POST /backend-api/f/conversation` | POST | Bearer + Sentinel + Conduit | ✅ SSE 生图流 |
| `GET /backend-api/conversation/{cid}` | GET | Bearer | ✅ 获取对话详情 |
| `GET /backend-api/conversation/{cid}/attachment/{sid}/download` | GET | Bearer | ✅ 获取图片下载 URL |
| `GET /backend-api/files/{fid}/download` | GET | Bearer | ✅ 备选下载方式 |

## 七、关键发现与修正

1. **生图端点不是 `/backend-api/conversation`**，而是 **`/backend-api/f/conversation`**（带 `/f/` 前缀）
   - `/backend-api/conversation` 是**文本聊天**端点
   - `/backend-api/f/conversation` 是**图片生成**端点
   - 两个端点 SSE 格式不同：文本聊天用直接格式 `{"message":{...}}`，生图用 patch 包装 `{"p":"","o":"add","v":{...}}`
2. **需要 prepare 步骤**：先 `/f/conversation/prepare` 获取 `conduit_token`，再 `/f/conversation`
3. **纯文本生图用 `content_type: "text"`**，不是 `multimodal_text`（后者用于带图片的多模态输入）
4. **`content_type: "text"` 在两个端点都可用** ✅
   - `/backend-api/conversation` + `content_type: "text"` → HTTP 200（普通文本对话）
   - `/backend-api/f/conversation` + `content_type: "text"` → HTTP 200（生图）
   - 原始 `curl_cffi_request.py` 的 422 错误是由其他字段或 headers 缺失导致，非 `content_type` 引起
5. **OAuth access_token 完全可用于生图**，无需 Web Session Cookie
6. **Cloudflare WAF 可通过 curl-cffi + edge101 指纹 + PoW 完全绕过**
7. **Turnstile token 当前不触发**（arkose.required = false）
8. **asset_pointer 使用 `sediment://` 协议**（不是 `file-service://`）
9. **图片下载通过 attachment 端点**，返回 estuary CDN URL
10. **parts 字段是纯字符串数组**（如 `["prompt text"]`），不是对象数组 `[{content_type: "text", text: "..."}]`
11. **生图请求不需要 `conversation_id`**，使用 `parent_message_id` 代替（后端会自动创建 conversation）
