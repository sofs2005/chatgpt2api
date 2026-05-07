# ChatGPT Images API 端点清单

> 更新: 2026-05-07 — 已通过实抓验证修正
> 标记: ✅ 已验证 | 🔶 推测 | ❌ 已证伪

## 一、页面加载时触发的 API (匿名用户)

### 1. GET `/backend-anon/images/styles` ✅
- **用途**: 获取可用图片风格预设列表
- **认证**: 匿名
- **缓存**: CDN (persistent.oaistatic.com)
- **响应**: JSON — 32 种风格配置

### 2. POST `/backend-anon/conversation/init` ✅
- **用途**: 初始化匿名对话
- **认证**: 匿名
- **响应**: `conversation_detail_metadata` — 限额 + 功能开关
- **关键字段**:
  - `blocked_features` — 被限制的功能
  - `default_model_slug` — 默认模型路由 ("auto")
  - `limits_progress` — 各项功能剩余次数

### 3. POST `/ces/v1/t` 🔶
- **用途**: 遥测/埋点数据上报
- **认证**: 匿名
- **频率**: 高频 (每个用户行为事件)

### 4. GET `/backend-anon/checkout_pricing_config/configs/JP` 🔶
- **用途**: 获取定价配置
- **认证**: 匿名

## 二、已登录用户的生图 API

### 5. POST `/backend-api/sentinel/chat-requirements` ✅
- **用途**: 获取 sentinel token + PoW 难度参数
- **认证**: Bearer Token + PoW (legacy token)
- **请求**:
```json
{
  "p": "gAAAAAC<legacy_requirements_token>"
}
```
- **响应**:
```json
{
  "token": "gAAAAAB...",        // sentinel token (后续所有请求需要)
  "proofofwork": {
    "required": true,
    "seed": "...",
    "difficulty": "000fffff..."
  },
  "turnstile": { "required": false },
  "expire_after": 3600
}
```

### 6. POST `/backend-api/f/conversation/prepare` ✅
- **用途**: 准备图片生成对话，获取 conduit_token
- **认证**: Bearer Token + Sentinel Token + PoW Proof Token
- **请求**:
```json
{
  "action": "next",
  "parent_message_id": "<uuid>",
  "model": "gpt-5-5",
  "client_prepare_state": "success",
  "timezone_offset_min": -480,
  "timezone": "Asia/Shanghai",
  "conversation_mode": { "kind": "primary_assistant" },
  "system_hints": ["picture_v2"],
  "partial_query": {
    "id": "<uuid>",
    "author": { "role": "user" },
    "content": {
      "content_type": "text",
      "parts": ["<prompt>"]
    }
  },
  "supports_buffering": true,
  "supported_encodings": ["v1"],
  "client_contextual_info": { "app_name": "chatgpt.com" }
}
```
- **响应**: `{"status": "ok", "conduit_token": "eyJ..."}`

### 7. POST `/backend-api/f/conversation` ✅ (核心生图端点)
- **用途**: 发起图片生成 (SSE 流式返回)
- **认证**: Bearer Token + Sentinel Token + PoW Proof Token + Conduit Token
- **类型**: SSE (Server-Sent Events)
- **Content-Type**: `text/event-stream; charset=utf-8`
- **必需 Headers**:
```http
X-OpenAI-Target-Path: /backend-api/f/conversation
X-OpenAI-Target-Route: /backend-api/f/conversation
OpenAI-Sentinel-Chat-Requirements-Token: <sentinel_token>
OpenAI-Sentinel-Proof-Token: gAAAAAB<proof>
X-Conduit-Token: <conduit_token>
X-Oai-Turn-Trace-Id: <uuid>
OAI-Device-Id: <uuid>
OAI-Session-Id: <uuid>
OAI-Language: zh-CN
OAI-Client-Version: prod-...
OAI-Client-Build-Number: ...
```
- **请求体**:
```json
{
  "action": "next",
  "messages": [{
    "id": "<uuid>",
    "author": { "role": "user" },
    "create_time": 1778122176.0,
    "content": {
      "content_type": "text",
      "parts": ["<prompt>"]
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
```
- **parts 字段说明**: parts 是**纯字符串数组** `["prompt text"]`，不是对象数组 `[{content_type: "text", text: "..."}]`
- **SSE 响应**: 有 3 种事件格式，详见 `authenticated-api-schema.md`

### 8. POST `/backend-api/conversation` ✅ (文本聊天端点)
- **用途**: 普通文本对话 (非生图)
- **与生图端点的关键区别**:
  - 不需要 prepare 步骤 (无需 conduit_token)
  - SSE 格式不同: 消息直接在顶层 `{"message": {...}}`，不在 patch 包装内 `{"p":"","o":"add","v":{...}}`
  - 不需要 `X-Conduit-Token` header
- **请求体** (与生图共享 content_type schema):
```json
{
  "action": "next",
  "messages": [{
    "id": "<uuid>",
    "author": { "role": "user" },
    "create_time": 1778123679.0,
    "content": {
      "content_type": "text",
      "parts": ["Hello"]
    }
  }],
  "model": "auto",
  "timezone": "Asia/Shanghai",
  "timezone_offset_min": -480
}
```

### 9. GET `/backend-api/conversation/<conversation_id>` ✅
- **用途**: 获取对话历史/消息树
- **认证**: Bearer Token
- **响应**: 包含 `mapping` 字段，每节点含 message 对象

### 10. GET `/backend-api/conversation/<cid>/attachment/<sid>/download` ✅
- **用途**: 获取图片下载 URL (sediment:// 资产解析)
- **认证**: Bearer Token
- **响应**: `{"download_url": "https://chatgpt.com/backend-api/estuary/content?id=..."}`
- **后续**: GET `{download_url}` 获取图片二进制

### 11. GET `/backend-api/files/<fid>/download` ✅
- **用途**: 备选图片下载方式 (file-service:// 资产)
- **认证**: Bearer Token
- **响应**: `{"download_url": "..."}` → 同上下载

## 三、图片库 API 🔶

### 12. 图片资产相关
- 图片上传: `POST /backend-api/files` (multipart)
- 图片库列表: `GET /backend-api/images?...`
- 图片归档: `PATCH /backend-api/conversation/<id>` body: `{is_archived: true}`

## 四、路由清单 (Remix Routes)

| Route Pattern | 说明 |
|---|---|
| `/images` | 图片 App 主页 |
| `/c/:conversationId` | 对话页面 |
| `/c/:conversationId/v/:n7jupdId` | 生图结果页面 |
| `/g/:gizmoId` | Gizmo 详情页 |

## 五、请求头特征

所有 API 请求携带:
```
OAI-Language: zh-CN
OAI-Device-Id: <uuid>
OAI-Session-Id: <uuid>
OAI-Client-Version: prod-<hash>
OAI-Client-Build-Number: <number>
Content-Type: application/json
sec-ch-ua: "Microsoft Edge";v="143", ...
sec-ch-ua-platform: "Windows"
sec-ch-ua-mobile: ?0
```

已登录请求额外携带:
```
Authorization: Bearer <oauth_access_token>
X-OpenAI-Target-Path: <path>
X-OpenAI-Target-Route: <path>
```

生图请求额外携带:
```
OpenAI-Sentinel-Chat-Requirements-Token: <sentinel_token>
OpenAI-Sentinel-Proof-Token: gAAAAAB<proof>
X-Conduit-Token: <conduit_token>        // 仅 /f/conversation
X-Oai-Turn-Trace-Id: <uuid>             // 仅 /f/conversation
```
