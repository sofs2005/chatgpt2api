# Sentinel chat-requirements 契约（单步 vs 三步）

> 抓取日期: 2026-09-29
> 目标: `https://chatgpt.com/backend-anon/sentinel/*`
> 方式: 直连探测（匿名 `oai-did`）+ 前端 `sdk.js` 静态分析
> 用途: 判断本仓库现有单步链路是否仍然可用，以及迁移三步流程需要哪些字段

## 结论摘要

**旧单步端点仍然可用，未过时。** 这修正了此前「上游已迁移三步、旧端点已废弃」的假设：

- `POST /backend-anon/sentinel/chat-requirements`（旧，单步）→ HTTP 200，返回完整 `token`；
- `POST /backend-anon/sentinel/chat-requirements/prepare`（新，三步第一步）→ HTTP 200，返回 `prepare_token`（**注意字段名不同**）；
- `POST /backend-anon/sentinel/chat-requirements/finalize`（新，三步最后一步）→ 端点存在且会校验 token（传入伪造 token 返回 500）。

因此迁移三步流程属于**可选增强**而非兼容性修复，本仓库现有单步实现可以继续使用。

## 前置条件

两个端点都要求带 `oai-did` cookie，否则 401：

```http
Cookie: oai-did=<uuid>
oai-device-id: <uuid>
oai-language: zh-CN
Content-Type: application/json
```

## 旧端点（单步，本仓库当前使用）

`POST /backend-anon/sentinel/chat-requirements`

请求体：

```json
{ "p": "gAAAAAC<requirements_token>" }
```

响应（实测结构）：

```json
{
  "persona": "chatgpt-noauth",
  "token": "gAAAAAB<...>",
  "expire_after": 540,
  "expire_at": 1780000000,
  "force_login": true,
  "turnstile":    { "required": true, "dx": "<~32KB 加密 blob>" },
  "proofofwork":  { "required": true, "seed": "0.38522512501402784", "difficulty": "06bf7d" },
  "so":           { "required": true, "collector_dx": "<~32KB>", "snapshot_dx": "<~26KB>" }
}
```

字段说明：

| 字段 | 含义 |
| --- | --- |
| `token` | 后续业务请求的 `OpenAI-Sentinel-Chat-Requirements-Token`，前缀 `gAAAAAB` |
| `expire_after` | 有效期（秒）；匿名实测 540 |
| `expire_at` | 绝对过期时间戳 |
| `force_login` | 匿名会话是否被要求登录 |
| `turnstile.dx` | Turnstile 求解输入（加密 blob） |
| `proofofwork.difficulty` | PoW 难度掩码（十六进制字符串，非十进制位数） |
| `so.collector_dx` / `snapshot_dx` | session observer 的两个输入 |

## 新端点（三步）

### 第一步 `prepare`

`POST /backend-anon/sentinel/chat-requirements/prepare`

请求体与旧端点相同：

```json
{ "p": "gAAAAAC<requirements_token>" }
```

响应（实测结构）——**与旧端点的差异集中在字段名**：

```json
{
  "persona": "chatgpt-noauth",
  "prepare_token": "gAAAAAB<...>",
  "turnstile":   { "required": true, "dx": "<~29KB>" },
  "proofofwork": { "required": true, "seed": "0.5803295893613895", "difficulty": "061a80" },
  "so":          { "required": true, "collector_dx": "<~31KB>", "snapshot_dx": "<~25KB>" }
}
```

与旧端点的差异：

- 返回 `prepare_token` 而非 `token`；
- 此步**不返回** `expire_after` / `expire_at` / `force_login`；
- `turnstile` / `proofofwork` / `so` 结构与旧端点一致。

### 第二步 `req`（未验证）

**尚未确认。** 前端 bundle 中未见该路径的实抓报文，`sdk.js` 内部只暴露
`/sentinel/` 作为基路径。此步是三步流程中唯一还没有契约的一环，
迁移前必须先补抓。

### 第三步 `finalize`

`POST /backend-anon/sentinel/chat-requirements/finalize`

请求体（来自前端 bundle 分析，**未通过实抓验证**）：

```json
{
  "prepare_token": "gAAAAAB<...>",
  "proofofwork":   "gAAAAAB<PoW 解答>",
  "turnstile":     "<Turnstile 解答>"
}
```

`proofofwork` 与 `turnstile` 按 `prepare` 返回的 `required` 决定是否携带。

验证情况：用伪造的 `prepare_token` 请求该端点返回
`{"detail":"Internal Server Error"}`（HTTP 500）。**注意**：空体、错字段名、
非字符串类型也同样返回 500，说明该端点缺少输入校验，因此**无法用错误响应反推
字段名**。字段名仍需从 bundle 或真实浏览器抓包确认。

## 请求头常量

| 头 | 来源 | 说明 |
| --- | --- | --- |
| `OpenAI-Sentinel-Chat-Requirements-Token` | 单步流程 | 现有实现使用 |
| `OpenAI-Sentinel-Proof-Token` | 单步流程 | PoW 解答，前缀 `gAAAAAB` |
| `OpenAI-Sentinel-Chat-Requirements-Prepare-Token` | 三步流程（bundle 分析） | 承载 `prepare_token` |

## SDK 加载链路

`GET /backend-api/sentinel/sdk.js` 是一个约 1.4KB 的**加载器存根**，
真正实现位于带版本号的路径：

```
GET /sentinel/20260219f9f6/sdk.js   → 30864 字节
```

存根中同时保留 `window.__sentinel_token_pending`、`window.__sentinel_init_pending`、
`window.__sentinel_script_loads` 三个全局状态位，用于在真正 SDK 到达前排队调用。

SDK 暴露的入口（静态分析）：

| 名称 | 用途 |
| --- | --- |
| `getRequirementsToken` | 异步获取 requirements token（前缀 `gAAAAAC`） |
| `getRequirementsTokenBlocking` | 同步阻塞版，供同步上下文使用 |
| `getEnforcementToken` | 获取 enforcement token（前缀 `gAAAAAB`） |
| `sessionObserverToken` | session observer；在 iframe 中调用会报错 |

存根内出现的字符串同时包括 `turnstile`、`then`、`search`、`cachedChatReq`、
`snapshot_dx`、`sessionObserverToken() should not be called from within an iframe.`，
可佐证 turnstile 与 session observer 仍在链路内。

## 对本仓库的影响

1. **无需紧急改动**：单步端点仍返回可用 `token`，现有 `ChatRequirements` 流程不受影响。
2. **迁移前置条件**：三步流程的第二步 `req` 契约缺失，`finalize` 字段名未验证。
   迁移前必须补齐这两项，否则无法保证 `finalize` 能拿到最终 token。
3. **字段名差异是主要陷阱**：`prepare` 返回的是 `prepare_token`，不是 `token`。
   直接复用现有解析逻辑会把 `token` 取成空串。
4. **PoW 难度语义未变**：`difficulty` 仍是十六进制掩码字符串（如 `06bf7d`），
   与现有 `pow.go` 的处理方式一致。
5. **匿名链路 `turnstile.required=true`**：与迁移前一致，`turnstile.go` 的 dx 求解
   仍是必要条件。

## 复现方式

```bash
DID=$(uuidgen)
curl -sS -X POST "https://chatgpt.com/backend-anon/sentinel/chat-requirements" \
  -H "Content-Type: application/json" \
  -H "oai-device-id: $DID" -H "oai-language: zh-CN" -b "oai-did=$DID" \
  -d '{"p":"gAAAAAC<requirements_token>"}'
```

将路径替换为 `/prepare` 即得三步流程第一步。注意：直连请求容易触发 Cloudflare
403，建议通过可信出口执行。

## 未验证项

- 三步流程第二步 `req` 的路径、请求体与响应体；
- `finalize` 的准确字段名（该端点无输入校验，错误响应无法反推）；
- `finalize` 成功响应中最终 token 的字段名与有效期字段；
- 三步流程相比单步是否真的提升通过率（需要 A/B 实测）。
