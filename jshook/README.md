# ChatGPT gpt-image-2 生图链路 JS 逆向分析

> 分析日期: 2026-05-07
> 目标: `https://chatgpt.com/images`
> 工具: jshook MCP (Chrome CDP + JS Hook + Network Interception)
> 工具链接: https://github.com/vmoranv/jshookmcp

本目录是 ChatGPT Web 图片生成链路的逆向资料索引，覆盖前端函数映射、请求体构造、认证/PoW/Sentinel、SSE 事件结构、资产下载、验证脚本和脱敏响应样本。上游行为变化较快，修改后端协议实现前应优先用 `jshook` 或 `jshook/scripts/` 重新验证。

## 快速入口

| 目的 | 首选文档 | 适用场景 |
| --- | --- | --- |
| 看完整技术结论 | [docs/ChatGPT-gpt-image-2-generation-pipeline-analysis.md](docs/ChatGPT-gpt-image-2-generation-pipeline-analysis.md) | 端到端理解 gpt-image-2 / ChatGPT Images 2.0 链路 |
| 查端点和请求头 | [docs/api-endpoints.md](docs/api-endpoints.md) | 添加或修正后端 HTTP 调用、请求头、路由 |
| 查认证生图 Schema | [docs/authenticated-api-schema.md](docs/authenticated-api-schema.md) | 实抓链路、PoW、Sentinel、Prepare、Generate、Download |
| 查请求完成链路 | [docs/request-completion-flow.md](docs/request-completion-flow.md) | 前端 OV/mp 调用链、callsite_id、SSE 差异 |
| 查 SSE 协议 | [docs/upstream-sse-conversation.md](docs/upstream-sse-conversation.md) | 解析 `/conversation` 和 `/f/conversation` 流式响应 |
| 查 content_type / recipient | [docs/content-type-enum.md](docs/content-type-enum.md) | 区分前端 `zo` 枚举和 API `content_type` |
| 查混淆函数名 | [docs/function-mapping.md](docs/function-mapping.md) | 从 JS bundle 定位上传、生图、渲染、埋点函数 |
| 查内部暗语 | [docs/internal-codenames.md](docs/internal-codenames.md) | 理解模型、工具、资产、channel、缓存 key 代号 |

## 按任务查找

| 任务 | 入口 |
| --- | --- |
| 实现或修复图片生成请求 | [docs/authenticated-api-schema.md](docs/authenticated-api-schema.md)、[docs/request-completion-flow.md](docs/request-completion-flow.md)、[scripts/image_gen_full_flow.py](scripts/image_gen_full_flow.py) |
| 实现或修复文本聊天请求 | [docs/api-endpoints.md](docs/api-endpoints.md)、[docs/upstream-sse-conversation.md](docs/upstream-sse-conversation.md)、[scripts/verify_text_chat.py](scripts/verify_text_chat.py) |
| 比较生图和文本 SSE 差异 | [docs/request-completion-flow.md](docs/request-completion-flow.md)、[responses/image-gen-sse-response.json](responses/image-gen-sse-response.json)、[responses/text-chat-sse-response.json](responses/text-chat-sse-response.json) |
| 更新模型路由、工具标识或 callsite | [docs/ChatGPT-gpt-image-2-generation-pipeline-analysis.md](docs/ChatGPT-gpt-image-2-generation-pipeline-analysis.md)、[docs/function-mapping.md](docs/function-mapping.md)、[docs/internal-codenames.md](docs/internal-codenames.md) |
| 验证匿名页面能力和风格列表 | [docs/api-endpoints.md](docs/api-endpoints.md)、[responses/images-styles.json](responses/images-styles.json)、[responses/conversation-init.json](responses/conversation-init.json) |
| 排查下载和资产指针 | [docs/authenticated-api-schema.md](docs/authenticated-api-schema.md)、[docs/upstream-sse-conversation.md](docs/upstream-sse-conversation.md)、[responses/file_00000000bc987209adba1c148413e076.png](responses/file_00000000bc987209adba1c148413e076.png) |

## 文档索引

### 总览与综合分析

- [docs/ChatGPT-gpt-image-2-generation-pipeline-analysis.md](docs/ChatGPT-gpt-image-2-generation-pipeline-analysis.md): 综合分析文档，覆盖架构、端点、认证体系、模型路由、请求体、生图/改图、SSE、质量控制、资产生命周期、Statsig 和待验证项。
- [docs/authenticated-api-schema.md](docs/authenticated-api-schema.md): 认证生图 API Schema，按 Bootstrap、Chat Requirements、PoW、Prepare、Generate、Download 六步记录实抓字段和关键修正。

### 协议与链路

- [docs/api-endpoints.md](docs/api-endpoints.md): API 端点清单，包含匿名页面接口、登录态生图接口、文本聊天接口、下载接口、图片库接口、Remix 路由和请求头特征。
- [docs/request-completion-flow.md](docs/request-completion-flow.md): 图片生成请求完成链路，记录前端调用链、OV 参数、callsite_id、请求体构建、SSE 处理和并发/异步模式。
- [docs/upstream-sse-conversation.md](docs/upstream-sse-conversation.md): 上游 Conversation SSE 协议说明，覆盖 patch 结构、消息 add、文本增量、图片工具成功、拒绝、moderation、marker、metadata 和结果判断。

### 前端枚举与函数映射

- [docs/content-type-enum.md](docs/content-type-enum.md): 前端渲染类型 `zo`、API `content_type`、生图 recipient、判断函数和 author 名称枚举。
- [docs/function-mapping.md](docs/function-mapping.md): ChatGPT Images 关键混淆函数映射，覆盖 composer 到 API、图片选择上传、消息渲染、判断函数、路由、图片库和埋点。
- [docs/internal-codenames.md](docs/internal-codenames.md): 内部代号和暗语词典，覆盖模型/工具、功能系统、路由、存储 key、消息 channel 和资产生命周期。

## 脚本索引

| 脚本 | 用途 | 产物 |
| --- | --- | --- |
| [scripts/image_gen_full_flow.py](scripts/image_gen_full_flow.py) | 完整认证生图链路验证：Bootstrap -> Chat Requirements -> PoW -> Prepare -> Generate SSE -> Download | `responses/image-gen-sse-response.json`、下载图片 |
| [scripts/verify_text_chat.py](scripts/verify_text_chat.py) | 验证 `/backend-api/conversation` 文本聊天端点，并与生图端点对比 | `responses/text-chat-sse-response.json` |
| [scripts/curl_cffi_request.py](scripts/curl_cffi_request.py) | 早期 curl-cffi 请求实验脚本，用于验证 TLS/浏览器指纹和基础请求体 | 调试输出或临时响应 |

脚本通常需要本地有效认证状态、网络访问和 `curl-cffi` 等 Python 依赖。不要把真实 OAuth token、cookie、账号信息、代理凭据或可复用下载 URL 写入脚本或响应样本。

## 响应样本索引

| 文件 | 内容 |
| --- | --- |
| [responses/images-styles.json](responses/images-styles.json) | 匿名风格列表，包含 32 种图片风格预设 |
| [responses/conversation-init.json](responses/conversation-init.json) | 匿名或会话初始化响应，含限额和能力相关信息 |
| [responses/image-gen-sse-response.json](responses/image-gen-sse-response.json) | 完整生图 SSE 响应样本，包含 `image_asset_pointer` 和图片资产事件 |
| [responses/text-chat-sse-response.json](responses/text-chat-sse-response.json) | 文本聊天 SSE 响应样本，用于对比 `/conversation` 与 `/f/conversation` |
| [responses/refreshed-tokens.json](responses/refreshed-tokens.json) | token 刷新响应样本；提交前必须确认已脱敏 |
| [responses/file_00000000bc987209adba1c148413e076.png](responses/file_00000000bc987209adba1c148413e076.png) | 实测生成图片样本 |

## 目录结构

```text
jshook/
├── README.md
├── docs/
│   ├── ChatGPT-gpt-image-2-generation-pipeline-analysis.md
│   ├── api-endpoints.md
│   ├── authenticated-api-schema.md
│   ├── content-type-enum.md
│   ├── function-mapping.md
│   ├── internal-codenames.md
│   ├── request-completion-flow.md
│   └── upstream-sse-conversation.md
├── scripts/
│   ├── curl_cffi_request.py
│   ├── image_gen_full_flow.py
│   └── verify_text_chat.py
└── responses/
    ├── conversation-init.json
    ├── file_00000000bc987209adba1c148413e076.png
    ├── image-gen-sse-response.json
    ├── images-styles.json
    ├── refreshed-tokens.json
    └── text-chat-sse-response.json
```

## 核心发现摘要

### 1. 模型路由

- `default_model_slug: "auto"`: 后端自动路由，前端不指定具体模型名。
- 页面标题显示 "ChatGPT Images 2.0"，当前链路推测对应 `gpt-image-2`。
- 实抓生图模型路由为 `gpt-5-5`，文本回复可出现 `i-5-mini-m`。

### 2. 生图工具标识

- **Recipient**: `t2uay3k.sj1i4kz`，实抓确认。
- **Author Name**: 旧版前端代码出现 `n7jupd_m`，当前实抓为 `t2uay3k.sj1i4kz`。
- **Content Type**: 请求可使用 `text`；响应图片消息中出现 `multimodal_text` 和 `image_asset_pointer` part。
- **Message Marker**: `user_visible_token`、`final_channel_token`、`last_token`。

### 3. API 端点

- **风格列表**: `GET /backend-anon/images/styles`，匿名。
- **对话初始化**: `POST /backend-anon/conversation/init`，匿名。
- **Chat Requirements**: `POST /backend-api/sentinel/chat-requirements`，需认证和 PoW。
- **Prepare Image**: `POST /backend-api/f/conversation/prepare`，需认证和 Sentinel，返回 `conduit_token`。
- **核心生图**: `POST /backend-api/f/conversation`，需认证、Sentinel 和 Conduit，SSE 流式返回。
- **文本聊天**: `POST /backend-api/conversation`，无需 prepare/conduit，SSE 格式不同。
- **图片下载**: `GET /backend-api/conversation/{cid}/attachment/{sid}/download`，返回 estuary CDN URL。

### 4. 请求链路

生图链路 (`/backend-api/f/conversation`):

```text
Bootstrap (GET / -> 提取 PoW 脚本)
  -> Chat Requirements (POST /sentinel/chat-requirements -> sentinel token + PoW 难度)
    -> Solve PoW (SHA3-512 brute force)
      -> Prepare (POST /f/conversation/prepare -> conduit_token)
        -> Generate (POST /f/conversation + SSE -> asset_pointer: sediment://...)
          -> Download (GET /attachment/{sid}/download -> CDN URL -> 图片)
```

文本聊天链路 (`/backend-api/conversation`):

```text
Bootstrap
  -> Chat Requirements
    -> Solve PoW
      -> POST /conversation (SSE -> 文本回复)
```

### 5. 匿名限制

- 匿名 `image_gen` 功能限额为 **0**，需要登录认证。
- 免费用户有基础限额，Plus/Pro 用户更高。

### 6. 特色功能

- 32 种预设风格: `scribble`、`anime`、`comic`、`icon`、`tarot` 等。
- 异步生成模式: `image_gen_async`。
- 多流并发: `image_gen_multi_stream`。
- 水印下载: `watermarked_asset_pointer`。
- Ghostrider 流式渲染引擎。

## 认证实测发现 (2026-05-07)

### Cloudflare / Sentinel

- `curl-cffi` + `edge101` 指纹 + PoW 可通过当时的 Cloudflare WAF 验证。
- OAuth Bearer Token 可用于实抓链路，无需 Web Session Cookie。
- Turnstile 当时未触发: `arkose.required = false`。

### 生图链路验证

- 准备端点: `POST /backend-api/f/conversation/prepare` -> `conduit_token`。
- 生图端点: `POST /backend-api/f/conversation`，不是 `/backend-api/conversation`。
- 模型映射: `gpt-image-2` -> `gpt-5-5`。
- 纯文本 prompt 可使用 `content_type: "text"`。
- SSE 响应返回 `image_asset_pointer`，资产指针使用 `sediment://`。
- 图片下载通过 `/backend-api/conversation/{cid}/attachment/{sid}/download` 获取 estuary CDN URL。

### 关键修正

- `/backend-api/conversation` 是文本聊天，`/backend-api/f/conversation` 是图片生成。
- 文本聊天 SSE 为直接 message 格式；生图 SSE 使用 patch 包装: `{"p":"","o":"add","v":{...}}`。
- 生图需要 prepare 步骤，先生成 `conduit_token` 再发起请求。
- `parts` 是字符串数组: `["prompt text"]`，不是对象数组。
- `sediment://` 用于图片资产指针，不同于 `file-service://`。
- 图片 `asset_pointer` 会直接出现在 SSE 事件中，无需额外轮询。

## 技术栈与前端线索

- **框架**: Remix / React Router v7。
- **UI**: React + Tailwind CSS。
- **状态管理**: React Query (TanStack Query) + Signals。
- **HTTP**: Fetch API with Proxy hook + EventSource (SSE)。
- **埋点**: Statsig + RudderStack + 自建 `ces/v1/t`。
- **安全**: Cloudflare + Arkose Token / Turnstile + Sentinel PoW。

## 维护规则

- 新抓包或新脚本结论优先写入对应 `docs/*.md`，再同步更新本索引。
- 实抓样本进入 `responses/` 前必须脱敏，尤其是 token、cookie、账号标识、conversation id、私密 prompt、代理信息和可复用下载 URL。
- 上游协议、模型路由、Statsig gate、PoW/Sentinel 字段均视为时效性信息；落地到后端实现前重新验证。
- 默认项目测试不运行 `jshook/scripts/`。只有修改逆向脚本、响应 fixture 或协议实现时，才按任务需要运行对应脚本或手动验证。
