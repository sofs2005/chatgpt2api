# ChatGPT `gpt-image-2` 生图链路技术分析文档

> **分析日期**: 2026-05-07
> **数据来源**: HAR 实抓 (`chatgpt.com_2026_05_07_14_35_51.har`) + jshook CDP 逆向 + curl-cffi 验证
> **标记**: ✅ 实抓验证 | 🔶 JS逆向推测 | ❌ 已证伪

---

## 一、架构概览

ChatGPT Images 基于 **Remix (React Router v7)** 框架构建的 SPA 应用。生图通过 `/backend-api/f/conversation` 端点（SSE 流式），底层模型路由至 `gpt-5-5`（display name: "ChatGPT Images 2.0"）。

```
┌──────────────────────────────────────────────────────┐
│                   chatgpt.com/images                 │
├──────────────────────────────────────────────────────┤
│  Remix SPA (React)                                   │
│  ├─ ImagesAppComposer (We)    — 输入组件              │
│  ├─ request_completion (fe/B) — 请求调度               │
│  ├─ OV()                      — 核心请求函数           │
│  └─ Wc.safePost()             — HTTP 客户端            │
├──────────────────────────────────────────────────────┤
│  Sentinel 认证层 (3步)                                │
│  ├─ /sentinel/chat-requirements/prepare              │
│  ├─ /sentinel/chat-requirements/finalize             │
│  └─ /sentinel/req (PoW验证)                          │
├──────────────────────────────────────────────────────┤
│  SSE EventSource                                     │
│  └─ t2uay3k.sj1i4kz 工具消息流处理                   │
└──────────────────────────────────────────────────────┘
```

---

## 二、核心 API 端点

### 2.1 端点总览 ✅

| 端点 | Method | 认证 | 说明 |
|---|---|---|---|
| `/backend-anon/images/styles` | GET | 匿名 | 32种风格预设 |
| `/backend-anon/conversation/init` | POST | 匿名 | 匿名对话初始化 |
| `/backend-api/conversation/init` | POST | Bearer | **认证对话初始化** |
| `/backend-api/sentinel/chat-requirements/prepare` | POST | Bearer + PoW | Sentinel 第1步 → `prepare_token` |
| `/backend-api/sentinel/chat-requirements/finalize` | POST | Bearer | Sentinel 第2步 → `token` |
| `/backend-api/sentinel/req` | POST | Bearer | PoW 验证 (可多次) |
| `/backend-api/sentinel/ping` | POST | Bearer | Sentinel keep-alive |
| `/backend-api/f/conversation/prepare` | POST | Bearer + Sentinel | 准备生图 → `conduit_token` |
| `/backend-api/f/conversation` | POST | Bearer + Sentinel + Conduit | **核心生图** SSE |
| `/backend-api/conversation` | POST | Bearer + Sentinel | 文本聊天 SSE |
| `/backend-api/conversation/{cid}` | GET | Bearer | 获取对话历史 |
| `/backend-api/conversation/{cid}/attachment/{sid}/download` | GET | Bearer | 图片下载 → estuary URL |
| `/backend-api/conversation/{cid}/stream_status` | GET | Bearer | 流状态查询 |
| `/backend-api/conversation/{cid}/textdocs` | GET | Bearer | 文本文档 |
| `/backend-api/files/download/{fid}` | GET | Bearer | 备选图片下载 |
| `/backend-api/files/library` | POST | Bearer | 文件库查询 |
| `/backend-api/beacons/home` | GET | Bearer | 首页信标 |
| `/backend-api/settings/is_adult` | GET | Bearer | 成人内容设置 |

### 2.2 认证版 `POST /backend-api/conversation/init` ✅

**响应** (Plus 账号实抓):
```json
{
  "type": "conversation_detail_metadata",
  "blocked_features": [],
  "model_limits": [],
  "limits_progress": [
    { "feature_name": "deep_research", "remaining": 25, "reset_after": "2026-06-06T..." },
    { "feature_name": "odyssey", "remaining": 40, "reset_after": "2026-06-06T..." },
    { "feature_name": "file_upload", "remaining": 80, "reset_after": "2026-05-07T..." },
    { "feature_name": "image_gen", "remaining": 113, "reset_after": "2026-05-07T..." }
  ],
  "default_model_slug": "gpt-5-5"
}
```

**关键差异**: 匿名版 `default_model_slug: "auto"`，Plus 版直接指定 `"gpt-5-5"`。

### 2.3 Sentinel 认证流 (3步) ✅

**Step 1: `POST /backend-api/sentinel/chat-requirements/prepare`**
```json
// Request
{ "p": "gAAAAAC..." }  // legacy PoW token (gAAAAAC 前缀)
// Response
{ "persona": "chatgpt-paid", "prepare_token": "gAAAAAB..." }
```

**Step 2: `POST /backend-api/sentinel/chat-requirements/finalize`**
```json
// Request
{ "prepare_token": "gAAAAAB..." }
// Response
{ "persona": "chatgpt-paid", "token": "gAAAAAB..." }  // 最终的 sentinel token
```

**Step 3: `POST /backend-api/sentinel/req`** (PoW 验证，可多次)
```json
// Request (与 prepare 相同格式的 PoW)
{ "p": "gAAAAAC..." }
// Response (交替返回)
{ "persona": "chatgpt-noauth", "token": "gAAAAAB..." }
// 或
{ "persona": "chatgpt-paid", "token": "gAAAAAB..." }
```

**与旧版的关键差异**:
| | 旧版 (我们之前用的) | 新版 (官方浏览器) |
|---|---|---|
| 端点 | `/sentinel/chat-requirements` | `/sentinel/chat-requirements/prepare` + `finalize` |
| 步骤 | 1步 | 3步 (prepare→finalize→req) |
| token前缀 | `gAAAAAB` | prepare返回 `gAAAAAB`, req返回 `gAAAAAB` |
| PoW前缀 | `gAAAAAB` | `gAAAAAC` (prepare), `gAAAAAB` (proof) |

---

## 三、完整请求头和认证体系 ✅

### 3.1 生图请求完整 Headers (`POST /backend-api/f/conversation`)

```http
:method: POST
:path: /backend-api/f/conversation
authorization: Bearer <oauth_jwt>
content-type: application/json
accept: text/event-stream
origin: https://chatgpt.com
referer: https://chatgpt.com/

# OAI 基础
oai-language: zh-CN
oai-device-id: <uuid>
oai-session-id: <uuid>
oai-client-version: prod-764aa0f61f6f796fa37647d706d548bb7b96f6da
oai-client-build-number: 6429938

# Sentinel 认证
openai-sentinel-chat-requirements-token: gAAAAAB...
openai-sentinel-proof-token: gAAAAAB...
openai-sentinel-turnstile-token: <base64_encoded_turnstile_token>

# 请求路由
x-openai-target-path: /backend-api/f/conversation
x-openai-target-route: /backend-api/f/conversation

# 生图特有
x-conduit-token: <jwt_conduit_token>
x-oai-turn-trace-id: <uuid>

# 完整性签名 (NEW!)
x-oai-is: ois1.<jwt_integrity_signature>

# 遥测
oai-telemetry: [1, null]
oai-echo-logs: 0,1100,1,2771,...

# Cookie (必需)
cookie: __Secure-next-auth.session-token.0=<encrypted_jwt>
cookie: __Secure-next-auth.session-token.1=<short_token>
cookie: oai-did=<device_id>
cookie: cf_clearance=<cloudflare_clearance>
cookie: __cf_bm=<cloudflare_bot_management>
cookie: oai-sc=<session_continuity>
cookie: __Secure-oai-is=<integrity_signature>
```

### 3.2 关键新发现 Headers

| Header | 说明 |
|---|---|
| `x-oai-is` | **请求完整性签名** — JWT 格式，防止请求篡改 |
| `openai-sentinel-turnstile-token` | **真实的 Turnstile token** — 之前以为不触发，实际在 headers 中存在 |
| `oai-telemetry` | 遥测数据 `[1, null]` |
| `oai-echo-logs` | 调试日志 ID 序列 |
| `x-conduit-token: no-token` | prepare 请求中显式发送 `no-token` |

---

## 四、模型路由 ✅

| 用户请求 | 实际 model slug | 来源 |
|---|---|---|
| `/images` 页默认 | `gpt-5-5` | HAR 实抓 (`default_model_slug`) |
| `/images` 页 prompt | `gpt-5-5` | HAR 实抓 (请求体 `model` 字段) |
| 文本回复 | `i-5-mini-m` | server_ste_metadata |
| Codex 链路 | `codex-gpt-image-2` → `gpt-5.4-mini` | JS 逆向 |
| 旧版/API直接调用 | `gpt-5-3` | 之前 jshook 测试 |

**关键发现**: 官方 /images 页面入口使用 `gpt-5-5` 而非 `gpt-5-3`。`auto` 模型由服务端自动路由到 `gpt-5-5`。

### 4.1 模型层级与门控

```
gpt-5-5          ← 默认生图模型 (所有已登录用户)
  └─ gpt-5-5-pro ← 门控变体 (Statsig feature gate 控制，更高分辨率潜力)
  
codex-gpt-image-2 → gpt-5.4-mini  ← Codex Responses 链路 (需 Plus/Team/Pro)
```

### 4.2 模型路由逻辑 (Go 后端实现)

```go
func officialImageModelSlug(model string) string {
    switch strings.TrimSpace(model) {
    case "gpt-image-2":      return "gpt-5-5"       // 官方图片工具
    case "codex-gpt-image-2": return "codex-gpt-image-2"  // Codex 链路
    case "", "auto":          return "auto"          // 服务端自动路由 → gpt-5-5
    default:                  return "auto"
    }
}
```

前端只有 `gpt-image-2` 和 `codex-gpt-image-2` 会触发生图工具调用；其余模型（`gpt-5`, `gpt-5-1`, `gpt-5.4`, `gpt-5.5` 等）作为对话模型提供。

---

## 五、生图请求体完整结构 ✅

### 5.1 首次生图 (从 /images 直接发起)

```json
{
  "action": "next",
  "messages": [{
    "id": "<uuid>",
    "author": { "role": "user" },
    "create_time": 1778135522.833,
    "content": {
      "content_type": "text",
      "parts": ["<用户 prompt 文本>"]
    },
    "metadata": {
      "developer_mode_connector_ids": [],
      "selected_github_repos": [],
      "selected_all_github_repos": false,
      "system_hints": ["picture_v2"],
      "serialization_metadata": { "custom_symbol_offsets": [] }
    }
  }],
  "parent_message_id": "client-created-root",
  "model": "gpt-5-5",
  "client_prepare_state": "success",
  "timezone_offset_min": -480,
  "timezone": "Asia/Shanghai",
  "conversation_mode": { "kind": "primary_assistant" },
  "enable_message_followups": true,
  "system_hints": ["picture_v2"],
  "supports_buffering": true,
  "supported_encodings": ["v1"],
  "client_contextual_info": {
    "is_dark_mode": false,
    "time_since_loaded": 24,
    "page_height": 945,
    "page_width": 1040,
    "pixel_ratio": 1,
    "screen_height": 1080,
    "screen_width": 1920,
    "app_name": "chatgpt.com"
  },
  "paragen_cot_summary_display_override": "allow",
  "force_parallel_switch": "auto"
}
```

### 5.2 追加生图 (对已有对话继续生图)

```json
{
  "action": "next",
  "fork_from_shared_post": false,
  "conversation_id": "<existing_cid>",
  "parent_message_id": "<existing_msg_id>",
  "model": "gpt-5-5",
  "client_prepare_state": "none",
  "timezone_offset_min": -480,
  "timezone": "Asia/Shanghai",
  "conversation_mode": { "kind": "primary_assistant" },
  "system_hints": [],
  "supports_buffering": true,
  "supported_encodings": ["v1"],
  "client_contextual_info": { "app_name": "chatgpt.com" }
}
```

### 5.3 关键差异对比

| 字段 | 首次生图 | 追加生图 |
|---|---|---|
| `parent_message_id` | `"client-created-root"` | 真实的 msg uuid |
| `conversation_id` | 无 | 已有的 cid |
| `client_prepare_state` | `"success"` | `"none"` |
| `messages` | 有 (含 prompt) | 无 |
| `system_hints` | `["picture_v2"]` | `[]` |
| `paragen_cot_summary_display_override` | `"allow"` | 无 |

---

## 六、图片编辑（改图）请求链路 ✅

### 6.1 模型：与生图共用

改图**不单独使用模型**，与生图使用相同的底层模型：

| 链路 | 生图模型 | 改图模型 |
|---|---|---|
| 官方 (`f/conversation`) | `gpt-5-5` | `gpt-5-5`（相同） |
| Codex (`codex/responses`) | `gpt-5.4-mini` | `gpt-5.4-mini`（相同） |

区分生图/改图的方式在请求体结构，不在模型。

### 6.2 官方链路改图请求体

与生图的核心差异：`content_type` 从 `"text"` 变为 `"multimodal_text"`，`parts` 中包含参考图的 `image_asset_pointer`：

```json
{
  "action": "next",
  "messages": [{
    "id": "<uuid>",
    "author": { "role": "user" },
    "create_time": 1778135522.833,
    "content": {
      "content_type": "multimodal_text",
      "parts": [
        {
          "content_type": "image_asset_pointer",
          "asset_pointer": "file-service://<uploaded_file_id>",
          "width": 1024,
          "height": 1024,
          "size_bytes": 245678
        },
        "<编辑 prompt 文本>"
      ]
    },
    "metadata": {
      "system_hints": ["picture_v2"],
      "attachments": [{
        "id": "<uploaded_file_id>",
        "mimeType": "image/png",
        "name": "image_1.png",
        "size": 245678,
        "width": 1024,
        "height": 1024
      }],
      "serialization_metadata": { "custom_symbol_offsets": [] }
    }
  }],
  "parent_message_id": "<uuid>",
  "model": "gpt-5-5",
  "client_prepare_state": "sent",
  "conversation_mode": { "kind": "primary_assistant" },
  "system_hints": ["picture_v2"],
  "supports_buffering": true,
  "supported_encodings": ["v1"],
  "client_contextual_info": { "app_name": "chatgpt.com" }
}
```

**带遮罩（mask）的改图** — 额外增加 `mask_pointer` 和 `mask_attachment`：

```json
{
  "content": {
    "content_type": "multimodal_text",
    "parts": [
      { "content_type": "image_asset_pointer", "asset_pointer": "file-service://<image_id>", ... },
      "<prompt>"
    ],
    "mask_pointer": "file-service://<mask_file_id>"
  },
  "metadata": {
    "attachments": [...],
    "mask_attachment": {
      "id": "<mask_file_id>",
      "mimeType": "image/png",
      "name": "mask.png",
      "size": 12345,
      "width": 1024,
      "height": 1024
    }
  }
}
```

### 6.3 Codex 链路改图请求体

Codex 链路通过 `tool.action` 切换，并支持 `input_image_mask`：

```json
{
  "model": "gpt-5.4-mini",
  "input": [{
    "role": "user",
    "content": [
      { "type": "input_text", "text": "<编辑 prompt>" },
      { "type": "input_image", "image_url": "data:image/png;base64,..." }
    ]
  }],
  "tools": [{
    "type": "image_generation",
    "action": "edit",
    "model": "gpt-5.4-mini",
    "size": "1024x1024"
  }],
  "tool_choice": { "type": "image_generation" },
  "instructions": "You generate and edit images for the user.",
  "stream": true
}
```

若存在遮罩图，额外增加：
```json
{ "input_image_mask": { "image_url": "data:image/png;base64,..." } }
```

### 6.4 改图上传流程

改图需要先将参考图上传到 OpenAI 文件服务（3 步）：

```
1. POST /backend-api/files
   → {"upload_url": "https://...", "file_id": "<file_id>"}

2. PUT {upload_url}  (Azure Blob Storage)
   Headers: x-ms-blob-type: BlockBlob, x-ms-version: 2020-04-08
   Body: 图片二进制

3. POST /backend-api/files/{file_id}/uploaded
   Body: {}
   → 文件就绪
```

### 6.5 生图 vs 改图关键差异

| 维度 | 生图 | 改图 |
|---|---|---|
| 底层模型 | `gpt-5-5` | `gpt-5-5`（相同） |
| `content_type` | `"text"` | `"multimodal_text"` |
| `parts` | `["prompt"]` | `[image_asset_pointer, ..., "prompt"]` |
| `metadata.attachments` | 无 | 参考图元数据列表 |
| `mask_pointer` | 无 | 有遮罩时存在 |
| `metadata.mask_attachment` | 无 | 有遮罩时存在 |
| `asset_pointer` 协议 | — | `file-service://` (已上传的参考图) |
| 前置上传 | 不需要 | 需要 (3步上传流程) |
| 输出 `asset_pointer` | `sediment://` | `sediment://`（相同） |
| `metadata.async_task_type` | `"image_gen"` | `"image_gen"`（相同） |

---

## 七、SSE 响应结构 ✅

### 7.1 生图端点 SSE (`/backend-api/f/conversation`)

三种事件格式:

**格式 A — Patch 事件 (消息包裹在 `v` 内)**
```json
{
  "p": "",
  "o": "add",
  "v": {
    "message": {
      "id": "...",
      "author": { "role": "tool", "name": "t2uay3k.sj1i4kz" },
      "content": {
        "content_type": "multimodal_text",
        "parts": [{
          "content_type": "image_asset_pointer",
          "asset_pointer": "sediment://file_xxx",
          "width": 1254, "height": 1254,
          "size_bytes": 2516385,
          "metadata": {
            "dalle": { "gen_id": "...", "prompt": "", "seed": null },
            "generation": {
              "gen_id": "...",
              "gen_size": "smimage",
              "gen_size_v2": "16",
              "width": 1254, "height": 1254,
              "orientation": "square"
            }
          }
        }]
      },
      "recipient": "all"
    },
    "conversation_id": "...",
    "error": null
  },
  "c": 2
}
```

**格式 B — 类型事件**
```json
{"type": "title_generation", "title": "...", "conversation_id": "..."}
{"type": "message_marker", "marker": "user_visible_token|final_channel_token|last_token", "event": "first|last", ...}
{"type": "message_stream_complete", "conversation_id": "..."}
{"type": "server_ste_metadata", "metadata": {"model_slug": "i-5-mini-m", "turn_use_case": "image gen", "plan_type": "plus", ...}}
```

**格式 C — 简写替换**
```json
{"p": "/message/status", "o": "replace", "v": "finished_successfully"}
```

### 7.2 图片尺寸实测

| Prompt | 返回尺寸 | gen_size | gen_size_v2 |
|---|---|---|---|
| "1:1正方形, 2k" | **1254×1254** | smimage | 16 |
| "serene zen garden" | **1536×1024** | smimage | 16 |
| "cute cat wizard hat" | **1254×1254** | smimage | 16 |

**结论**: `gen_size_v2: "16"` (smimage) 是当前唯一观察到的值。即使 prompt 中要求 "2k"，也不会改变 gen_size_v2。

### 7.3 文本聊天 SSE (`/backend-api/conversation`) 

**格式不同** — 消息直接在顶层:
```json
{
  "message": {
    "id": "...",
    "author": { "role": "assistant" },
    "content": { "content_type": "text", "parts": ["Hi! 👋"] },
    "status": "finished_successfully",
    "end_turn": true
  },
  "conversation_id": "...",
  "error": null
}
```

---

## 八、质量控制与分辨率分析 ✅

### 8.1 核心结论：无显式分辨率控制字段

官方 ChatGPT Images 2.0 (`gpt-5-5`) **不存在** `gen_size`、`gen_size_v2`、`quality`、`resolution`、`hd`、`upscale` 等请求体字段。无论 prompt 中是否包含 "2k"、"4k"、"high resolution" 等提示，`gen_size_v2` 始终为 `"16"` (smimage)。

### 8.2 唯一的质量相关参数：`system_hints: ["picture_v2"]`

```json
// 在请求体顶层
{ "system_hints": ["picture_v2"] }

// 在 message.metadata 中也存在
{ "metadata": { "system_hints": ["picture_v2"], ... } }
```

`picture_v2` 是一个特性开关/提示，启用图片生成 v2 管线。对比首次生图与追加生图：
- **首次生图**: `system_hints: ["picture_v2"]` — 启用 v2 管线
- **追加生图**: `system_hints: []` — 沿用已有管线

### 8.3 gen_size_v2 枚举

| 值 | 名称 | 观察状态 |
|---|---|---|
| `"16"` | smimage (标准) | ✅ 唯一观察到的值 |
| `"32"` | (推测中等尺寸) | ❌ 未观察到 |
| `"64"` | (推测大尺寸) | ❌ 未观察到 |

所有实抓图片均为 `gen_size: "smimage"`, `gen_size_v2: "16"`，输出尺寸由模型根据 prompt 描述自主决定（如 1254×1254 正方形、1536×1024 横版）。

### 8.4 宽高比控制：Prompt 模板而非请求字段

宽高比变更不通过请求体字段控制，而是通过 Statsig 配置中的预置 prompt 模板以**追加对话**方式实现：

```
"Please regenerate the image in a landscape (horizontal) aspect ratio."
"Please regenerate the image in a portrait (vertical) aspect ratio."
"Please regenerate the image in a square (1:1) aspect ratio."
"Please regenerate the image in a widescreen (16:9) aspect ratio."
"Please regenerate the image in a vertical (9:16) aspect ratio."
```

在 `internal/backend/responses_image.go` 中，比例参数通过中文 prompt 提示词注入：
```go
hints := map[string]string{
    "1:1":  "输出为 1:1 正方形构图，主体居中，适合正方形画幅。",
    "3:2":  "输出为 3:2 横版构图，适合摄影、产品展示和横向叙事画幅。",
    "16:9": "输出为 16:9 横屏构图，适合宽画幅展示。",
    // ...
}
```

### 8.5 图片输出尺寸（从 SSE 响应元数据获取）

```json
{
  "width": 1254, "height": 1254,
  "metadata": {
    "generation": {
      "gen_id": "67127a38-...",
      "gen_size": "smimage",
      "gen_size_v2": "16",
      "width": 1254, "height": 1254,
      "orientation": "square",
      "transparent_background": false
    },
    "container_pixel_height": 1254,
    "container_pixel_width": 1254
  }
}
```

实际输出尺寸为 16 的整数倍（`responsesImageSizeMultiple = 16`），最大边长 3840px，最大像素数 8,294,400。

---

## 九、资产生命周期 ✅

```
1. Sentinel Prepare  → /sentinel/chat-requirements/prepare → prepare_token
2. Sentinel Finalize → /sentinel/chat-requirements/finalize → token
3. Sentinel Req      → /sentinel/req (PoW验证, 多次)
4. Prepare Image     → /f/conversation/prepare → conduit_token
5. Generate          → /f/conversation (SSE) → sediment:// asset_pointer
6. Download          → /conversation/{cid}/attachment/{sid}/download → estuary CDN URL
7. Fetch             → GET {estuary_url} → 图片二进制
```

实际 estuary URL 示例:
```
GET /backend-api/estuary/content?id=file_xxx&ts=493926&p=fs&cid=1&sig=<sha256>&v=0
```

---


## 十、内部代号与术语映射

| 内部代号 | 实际含义 | 验证 |
|---|---|---|
| `t2uay3k.sj1i4kz` | 当前生图工具 author/recipient | ✅ 实抓 |
| `n7jupd_m` | 旧版生图工具 Author (前端代码中) | 🔶 JS逆向 |
| `gpt-5-5` | /images 页默认生图模型 | ✅ HAR实抓 |
| `gpt-5-5-pro` | 门控高分辨率生图变体 (Statsig gate) | 🔶 特性开关推断 |
| `gpt-5-3` | gpt-image-2 底层模型 (旧版路由，已弃用) | ✅ 之前测试 |
| `gpt-5.4-mini` | Codex Responses 链路生图模型 | ✅ 代码常量 |
| `auto` | 服务端自动模型路由 → `gpt-5-5` | ✅ HAR实抓 |
| `i-5-mini-m` | 图片描述/追问文本模型 | ✅ server_ste_metadata |
| `picture_v2` | system_hints 生图 v2 管线开关 | ✅ HAR实抓 |
| `gen_size_v2: "16"` | smimage 标准图片尺寸 | ✅ 多次实抓 |
| `gen_size: "smimage"` | 标准图片尺寸类别 | ✅ SSE响应 |
| `sediment://` | 图片资产指针协议 (生图结果引用) | ✅ 实抓 |
| `file-service://` | 文件资产指针协议 (上传图片引用) | ✅ 实抓 |
| `conduit_token` | prepare 端点返回的会话令牌 (JWT) | ✅ 实抓 |
| `turn_use_case: "image gen"` | 标记当前对话轮次为生图用途 | ✅ server_ste_metadata |
| `plan_type_bucket: "paid"` | 付费用户桶 (plus/pro) | ✅ server_ste_metadata |
| `client_prepare_state` | 客户端准备状态: `success`(首次) / `sent`(生成) / `none`(追加) | ✅ HAR实抓 |

---

## 十一、反爬与安全机制 ✅

### 11.1 Cloudflare
- 全站 Cloudflare 保护
- 需要 `cf_clearance` + `__cf_bm` cookie
- **curl-cffi + edge101 指纹 + PoW 可绕过** ✅

### 11.2 Sentinel 认证系统 (新版3步)
- `prepare` → `finalize` → `req` (PoW验证)
- `sentinel/ping` keep-alive
- Turnstile token 在 headers 中存在 (`openai-sentinel-turnstile-token`)

### 11.3 请求完整性签名
- `x-oai-is`: `ois1.<jwt>` — 请求完整性校验
- `__Secure-oai-is` cookie — 对应的 cookie 侧签名

### 11.4 客户端指纹
- `oai-client-version: prod-764aa0f61f6f796fa37647d706d548bb7b96f6da`
- `oai-client-build-number: 6429938`
- `sec-ch-ua` headers 完整 Chrome 147 指纹

---

## 十二、Statsig 特性开关 ✅

Statsig 是 ChatGPT 使用的特性开关/实验平台。以下特性开关通过 jshook CDP 从 `chatgpt.com/images` 页面实抓提取。

### 12.1 图片生成核心开关

| Feature Gate | 值 | 说明 |
|---|---|---|
| `image_resize_enabled` | `false` | **图片缩放/分辨率调整未启用** |
| `variable_image_height_enabled` | `false` | **可变图片高度未启用** |
| `use_default_model` | `true` | 使用默认模型（`auto` → 服务端路由） |
| `dalle_limit` | `-1` | DALL-E 调用限制（`-1` = 已认证用户无限） |
| `image_gen_async` | — | 异步生成模式 |
| `image_gen_multi_stream` | — | 多流并发生成 |

### 12.2 质量相关结论

**`image_resize_enabled: false`** 和 **`variable_image_height_enabled: false`** 表明：当前 ChatGPT Images 2.0 尚未开放 "High resolution" 用户可切换选项。所有生图均以 `gen_size_v2: "16"` (smimage) 输出，分辨率由模型自主决定。

`dalle_limit: -1` 表示已登录用户无调用次数限制（但服务端仍有 `image_gen` 限额进度跟踪，Plus 账号约 113 次/天）。

### 12.3 宽高比切换实现

宽高比变更通过 Statsig 配置的预置 prompt 模板以追加对话方式实现：

| 目标比例 | 预置 Prompt 模板 |
|---|---|
| 横版 (Landscape) | `"Please regenerate the image in a landscape (horizontal) aspect ratio."` |
| 竖版 (Portrait) | `"Please regenerate the image in a portrait (vertical) aspect ratio."` |
| 正方形 (Square) | `"Please regenerate the image in a square (1:1) aspect ratio."` |
| 宽屏 (Widescreen) | `"Please regenerate the image in a widescreen (16:9) aspect ratio."` |
| 竖屏 (Vertical) | `"Please regenerate the image in a vertical (9:16) aspect ratio."` |

### 12.4 gpt-5-5-pro 门控

Statsig 中存在 `gpt-5-5-pro` 模型的 feature gate。该模型可能是更高分辨率或更高质量的生图模型变体，通过 Statsig 实验控制特定用户群的访问权限。

---

## 十三、关键差异总结 (我们 vs 官方)

| 项目 | 我们当前实现 | 官方浏览器 (HAR) |
|---|---|---|
| 模型 | `gpt-5-5` ← 已更新 ✅ | `gpt-5-5` |
| system_hints | `["picture_v2"]` ← 已添加 ✅ | `["picture_v2"]` |
| Sentinel | 1步 (`/chat-requirements`) | 3步 (`prepare`→`finalize`→`req`) |
| Turnstile | 未发送 | `openai-sentinel-turnstile-token` header |
| 完整性签名 | 无 | `x-oai-is` + `__Secure-oai-is` |
| Client Version | `be885ab...` build 5955942 | `764aa0f6...` build 6429938 |
| conversation/init | `/backend-anon/...` | `/backend-api/...` (认证版) |
| 图片分辨率控制 | 无显式字段 | 同样无显式字段 (都是 prompt 控制) |
| gen_size | `smimage` / `16` | `smimage` / `16` (相同) |

---

## 十四、未解决问题与待验证项

1. **gpt-5-5-pro 模型**: Statsig 中存在 `gpt-5-5-pro` feature gate，但尚无实抓数据验证其是否存在更高的分辨率输出（如 `gen_size_v2: "32"` 或更大）。

2. **gen_size_v2 更高枚举值**: 除 `"16"` (smimage) 外，可能还存在 `"32"`, `"64"` 等更高分辨率的值，但当前 UI 未暴露切换入口（`image_resize_enabled: false`）。

3. **`x-oai-is` 签名生成算法**: 请求完整性签名的生成逻辑尚未逆向。

4. **Sentinel 3步 vs 1步**: 当前实现仍使用 1步 `/chat-requirements`，官方已迁移至 3步 (`prepare`→`finalize`→`req`)。现有 1步端点仍在工作中，但未来可能被废弃。

5. **`openai-sentinel-turnstile-token`**: 官方 HAR 中存在 Turnstile token header，但当前实际请求中 `arkose.required = false` 表示尚未强制触发。

> **注意**: 本文档仅用于技术研究和学习目的。所有 REDACTED 标记代表已脱敏的个人身份信息。
