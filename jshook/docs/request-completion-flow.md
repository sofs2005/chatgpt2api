# 图片生成请求完成链路 (Request Completion Flow)

> 标记: ✅ 已验证 | 🔶 推测 (从 minified JS 逆向) | ❌ 已证伪

## 一、完整调用链路 🔶

```
User Input (prompt text)
  │
  ├─ [未登录] T() → 跳转登录
  │
  └─ [已登录]
      │
      ▼
    We() — images_app_composer 组件
      │  props: { conversation, onComposerInteraction, promptOverride, ... }
      │
      ├─ k() — 第一次提交 (callsiteId: ...images_app_composer.1)
      │
      └─ j() — 第二次提交 (callsiteId: ...images_app_composer.2)
           j = async e => {
             if (!A()) { await T(e); return }
             b.logEventWithStatsig("chatgpt_web_image_library_composer_submitted")
             B({ callsiteId: "...2", conversation: l, ...await _e(e) })
           }
           │
           ▼
         _e(e) — 准备图片生成请求参数
           │
           ▼
         B() → fe() → OV() — 请求调度层
           │
           ▼
         实际 API 调用链:
           1. POST /backend-api/sentinel/chat-requirements → sentinel token + PoW params
           2. Solve PoW (SHA3-512)
           3. POST /backend-api/f/conversation/prepare → conduit_token
           4. POST /backend-api/f/conversation (SSE) → 图片 asset_pointer
           5. GET /backend-api/conversation/{cid}/attachment/{sid}/download → download_url
           6. GET {download_url} → 图片二进制
```

## 二、OV() 函数参数结构 🔶

```typescript
// async function OV(e, t = fx()) — 从压缩 JS 逆向推测
interface OVParams {
  conversation: ConversationObject;
  completionType: CompletionType;      // Fh.Next = "next"
  sourceEvent?: Event;
  eventSource?: string;               // "chat"
  completionMetadata?: CompletionMetadata;
  
  // 核心参数
  callsiteId: string;
  requestedModelId?: string;          // "auto" (默认后端路由)
  thinkingEffort?: string;
  
  // 消息
  promptMessage?: PromptMessage;
  prependMessages?: Message[];
  parentMessageIdPromise?: Promise<string>;
  
  // 特殊模式
  isReasoningSkipped?: boolean;
  forceParagen?: boolean;
  enableParagen?: boolean;
  
  // 回调
  callbacks?: {
    onImageGenMessage?: (msg: Message) => void;
    onCompletionSuccess?: () => void;
    onCompletionFailure?: (error: Error) => void;
  };
  
  // 项目
  projectSelectionEntrySurface?: string;
}
```

## 三、Callsite ID 完整清单 🔶

```js
// 图片 App Stage
"request_completion.tool_landing_pages.image_gen.images_app_stage.1"

// 图片 App Composer 入口
"request_completion.tool_landing_pages.image_gen.images_app.images_app_composer.1"

// 图片 App Composer 提交
"request_completion.tool_landing_pages.image_gen.images_app.images_app_composer.2"

// 对话内图片生成
"request_completion.images.image_gen_conversation_generation.1"

// Codex 图片生成
"request_completion.images.codex_image_generation.1"

// Prompt textarea
"request_completion.prompt_textarea.use_create_completion_from_suggestion.1"
```

## 四、请求体构建 (mp 函数) 🔶

```js
// mp(D, N) — 从压缩 JS 推测的 API 请求体构建函数
// D = 对话上下文参数, N = 消息 + 配置参数
function mp(contextParams, messageParams) {
  let body = {
    action: "next",
    parent_message_id: contextParams.parent_message_id,  // 不是 conversation_id
    model: messageParams.model || "auto",
    messages: [...prependMessages, promptMessage],
    timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
    timezone_offset_min: -new Date().getTimezoneOffset(),
  };
  
  // 生图特有参数
  if (messageParams.system_hints) body.system_hints = messageParams.system_hints;
  if (messageParams.force_parallel_switch) body.force_parallel_switch = messageParams.force_parallel_switch;
  
  return body;
}
```

**注意**: 上述结构是从前端压缩 JS 逆向推测的。实抓验证中使用的完整请求体有所不同，详见 `authenticated-api-schema.md` 的完整请求体。

## 五、SSE 响应流处理 ✅

### 5.1 生图端点 SSE (`/backend-api/f/conversation`)

有 **3 种事件格式**:

**格式 A — Patch 事件 (消息传递)**
```json
{
  "p": "",                    // JSON Pointer path (空 = 根)
  "o": "add",                 // 操作: add | replace
  "v": {
    "message": {              // 消息内容在 v 内部
      "id": "...",
      "author": { "role": "tool", "name": "t2uay3k.sj1i4kz" },
      "content": {
        "content_type": "multimodal_text",
        "parts": [{
          "content_type": "image_asset_pointer",
          "asset_pointer": "sediment://file_xxx",
          "width": 1536, "height": 1024,
          "size_bytes": 3541231,
          "metadata": { "generation": {...}, "dalle": {...} }
        }]
      }
    },
    "conversation_id": "...",
    "error": null
  },
  "c": 2                      // 计数器
}
```

**格式 B — 类型事件**
```json
// title_generation
{"type": "title_generation", "title": "...", "conversation_id": "..."}

// message_marker
{"type": "message_marker", "conversation_id": "...", "message_id": "...",
 "marker": "user_visible_token|final_channel_token|last_token", "event": "first|last"}

// message_stream_complete
{"type": "message_stream_complete", "conversation_id": "..."}

// server_ste_metadata
{"type": "server_ste_metadata", "metadata": {...}, "conversation_id": "..."}

// resume_conversation_token
{"type": "resume_conversation_token", "kind": "topic", "token": "...", "conversation_id": "..."}
```

**格式 C — 简写替换**
```json
{"p": "/message/status", "o": "replace", "v": "finished_successfully"}
```

**首个事件**: `"v1"` (字符串，表示编码版本)

**末尾事件**: `[DONE]`

### 5.2 文本聊天端点 SSE (`/backend-api/conversation`)

格式不同 — **消息直接在顶层**，无 patch 包装:

```json
{
  "message": {
    "id": "...",
    "author": { "role": "assistant" },
    "content": { "content_type": "text", "parts": ["Hi! 👋"] },
    "status": "finished_successfully",
    "end_turn": true,
    "metadata": {
      "finish_details": { "type": "stop", "stop_tokens": [200002] },
      "resolved_model_slug": "i-5-mini-m"
    }
  },
  "conversation_id": "...",
  "error": null
}
```

类型事件格式与生图端点相同。

### 5.3 事件序列对比

| 生图事件序列 | 文本聊天事件序列 |
|---|---|
| `"v1"` (编码版本) | `resume_conversation_token` |
| `resume_conversation_token` | 系统消息 (hidden) |
| 模型上下文 (`model_editable_context`) | 用户消息回显 |
| 工具调用 (`code` → `t2uay3k.sj1i4kz`) | `input_message` |
| 状态更新 (`finished_successfully`) | 模型上下文 (`model_editable_context`) |
| `title_generation` | 空 assistant 消息 (in_progress) |
| `message_marker` (user_visible_token) | `message_marker` (user_visible_token) |
| **tool 消息** (`multimodal_text` + `image_asset_pointer`) | assistant 消息 (finished, 含回复文本) |
| tool 追问文本 (`text`) | `message_marker` (last_token) |
| `message_marker` (final_channel_token) | `title_generation` |
| assistant 回复文本 | `server_ste_metadata` |
| `message_marker` (last_token) | `message_stream_complete` |
| `title_generation` (最终) | `[DONE]` |
| `server_ste_metadata` | |
| `message_stream_complete` | |
| `[DONE]` | |

## 六、客户端阶段流转 🔶

```
PLACEHOLDER_RENDERED
  │  (显示骨架屏/占位)
  ▼
FIRST_TOKEN_RENDERED
  │  (首张图片/token 到达)
  ▼
SEGMENT_RENDERED
  │  (分段渐进渲染, 多图片或高清分段)
  ▼
GENERATION_COMPLETED
  │  (全部图片生成完毕)
  ▼
(可选) ERROR — 生成失败
```

## 七、关键特性 gating

```js
// Feature Name: "image_gen"
const IMAGE_GEN = "image_gen";

// 功能检测: de() — 启用了 image_gen 的用户
function de(e) {
  return e.feature_name === IMAGE_GEN;
}

// 模型路由: default_model_slug = "auto" 
// → gpt-image-2 → gpt-5-5 (后端映射)
// 文本回复: i-5-mini-m

// 速率限制标识
const IMAGE_GEN_RATE_LIMIT = "image_gen_rate_limit";
```

## 八、并发与异步模式 🔶

支持的图片生成模式:
- `image_gen_async`: 异步生成
- `image_gen_multi_stream`: 一次请求生成多张图片
- 每张图片有独立的 `generation_id`
- 图片通过 `asset_pointer` (sediment:// 协议) 关联到消息

## 九、重要区分: 前端 zo 枚举 vs API content_type

| 概念 | 说明 | 示例 |
|---|---|---|
| **zo 枚举** | 前端渲染组件类型，决定用哪个 React 组件渲染消息 | `zo.t2uay3k` → `WY` (ImageGenCard) |
| **API content_type** | 后端 Pydantic 模型的 discriminator 字段 | `"text"`, `"multimodal_text"`, `"image_asset_pointer"`, `"code"` |
| **SSE event type** | SSE 流的顶层事件类型字段 | `"title_generation"`, `"message_marker"`, `"message_stream_complete"` |

**注意**: `request-completion-flow.md` 原版本将这三者混淆，特别是将 `zo.t2uay3k` 当作 SSE event type，这是错误的。`t2uay3k` 是前端渲染类型，出现在消息的 `recipient` 字段（如 `"t2uay3k.sj1i4kz"`），不在 SSE 的顶层 `type` 字段中。
