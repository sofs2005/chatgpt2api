# Sentinel chat-requirements 契约（单步 vs 三步）

> 抓取日期: 2026-09-29
> 目标: `https://chatgpt.com/backend-anon/sentinel/*`
> 方式: 直连探测（匿名 `oai-did`）+ 前端 bundle `4813494d-nsss7fxurseygool.js` 函数级还原
> 用途: 判断本仓库现有单步链路是否仍然可用，以及迁移三步流程需要哪些字段

## 结论摘要

**旧单步端点仍然可用，未过时。** 这修正了此前「上游已迁移三步、旧端点已废弃」的假设：

- `POST /backend-anon/sentinel/chat-requirements`（旧，单步）→ HTTP 200，返回完整 `token`；
- `POST /backend-anon/sentinel/chat-requirements/prepare`（新，三步第一步）→ HTTP 200，返回 `prepare_token`（**注意字段名不同**）；
- `POST /backend-anon/sentinel/chat-requirements/finalize`（新，三步最后一步）→ 端点存在且会校验 token（传入伪造 token 返回 500）。

因此迁移三步流程属于**可选增强**而非兼容性修复，本仓库现有单步实现可以继续使用。

**关键修正（2026-10-09 实抓，已登录档）**：三步流程的第二步**是 HTTP 端点**。
实抓中出现四条 sentinel 路径 —— `.../prepare`、`/sentinel/req`、`.../finalize`、`/sentinel/ping`（另有 `/sentinel/heartbeat`），
`POST /backend-api/sentinel/req` 确实存在（本次抓包命中 3 次，均 200，返回完整 `token` + `expire_after:540`）。
这证伪了此前「不存在 `/sentinel/req`、第二步只是浏览器内本地求解」的结论。

实测链路：`chat-requirements/prepare`（返回 `prepare_token` + `proofofwork` + `turnstile` + `so`）
→ 本地 PoW / Turnstile / SO 求解
→ `sentinel/req`（请求体 `{"p","id","flow":"conversation"}`，再返回一轮 `proofofwork` + `turnstile`）
→ `chat-requirements/finalize`（请求体 `{prepare_token, proofofwork, turnstile}`，返回最终 `token`）
→ `sentinel/ping` 保活（401 时前端重跑整条链）。

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

三步流程的完整实现位于 bundle 函数 `$Xt(chatReq, prefetchSource, authOption)`，
返回 `{chatReq, preparedPromise, finalizedPromise}`。

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

### 第二步：本地求解（**无 HTTP 请求**）

前端在 `prepare` 响应到手后，用它与**同一个响应对象**并行生成两个 enforcement token：

```js
let [proofToken, turnstileToken] = await Promise.all([
  y2.getEnforcementToken(t, { forceSync: true }),  // PoW
  R2.getEnforcementToken(t),                       // Turnstile
]);
```

**PoW 生成器**（`class TYt`，实例 `y2`）：

- 前缀固定 `gAAAAAB`（`_getAnswer` 内部 `let n = 'gAAAAAB'`）；
- 三条前置守卫：`proofofwork.required === false` → 返回 `null`；`seed` 或 `difficulty` 非字符串 → 返回 `null`；
- 按 `seed` 记忆化（`answers: Map`），同一 seed 只解一次；
- `maxAttempts = 5e5`；命中判据 `hash(answer).substring(0, difficulty.length) <= difficulty`，
  成功返回 `answer + '~S'`，即 **difficulty 是十六进制掩码字符串**，与旧端点一致。

**Turnstile 生成器**（`class XXt`，实例 `R2`）：

- `turnstile.required === true` 但 `dx` 缺失 → **直接抛错**（`Chat requirements requested a VM challenge without a payload.`）；
- `required === false` → 返回 `null`；
- Promise 缓存键 `` `${conversationId}::${dx}` ``，无 conversationId 时退化为 `dx` 本身，
  求解完成即从缓存删除（`finally` 中 `dxPromiseCache.delete`）。

两者都是**浏览器内计算**（PoW 暴力破解 + Turnstile VM），求解产物随 `finalize` 请求体提交。
**但这不代表没有中间 HTTP 往返**：`prepare` 与 `finalize` 之间还有一次 `POST /sentinel/req`
（见上文 2026-10-09 修正），浏览器在此拿回第二轮 `proofofwork` + `turnstile` 才继续求解。

### 第三步 `finalize`

`POST /backend-anon/sentinel/chat-requirements/finalize`

请求体（**bundle 还原，已确认**）：

```js
let body = { prepare_token: t.prepare_token ?? '' };
if (proofToken)     body.proofofwork = proofToken;
if (turnstileToken) body.turnstile    = turnstileToken;
```

即：

```json
{
  "prepare_token": "gAAAAAB<...>",
  "proofofwork":   "gAAAAAB<PoW 解答>~S",
  "turnstile":     "<Turnstile 解答>"
}
```

`proofofwork` 与 `turnstile` **按有无决定是否携带**（`prepare` 的 `required` 为 false 时对应字段缺席），
不是带 `null` 占位。

响应处理（**已确认**）：

```js
let c = await K.safePost('/sentinel/chat-requirements/finalize', {requestBody: body, authOption: n});
Object.assign(t, c);        // 直接合并进 chatReq 对象
t.persona = c.persona;      // persona 单独再取一次
```

`Object.assign` 说明 **finalize 的响应字段名与 prepare 的字段名共用一个命名空间**：
最终 token 会以 `c.token` 的形式盖写到 `chatReq` 上（与单步同名），
因此调用方读取 `chatReq.token` 的代码在两种模式下无需分叉。

验证情况：用伪造的 `prepare_token` 请求该端点返回
`{"detail":"Internal Server Error"}`（HTTP 500）。**注意**：空体、错字段名、
非字符串类型也同样返回 500，说明该端点缺少输入校验，因此**无法用错误响应反推
字段名** —— 上面的字段名来自 bundle 还原，不是错误响应推断。

### 逃生开关

`$Xt` 内有一个短路分支：若 `chatReq.__skip_chatreq_finalize` 为真，
则**跳过 finalize 请求**，直接把 Turnstile 解答写入本地缓存并返回，同时打点
`chat_requirements_finalize_skipped`。该字段由调用方在构造 chatReq 时置位（bundle 中未见
赋值点，推测来自服务端下发或 A/B 分流）。对后端的含义是：**上游自身保留了「不发 finalize」
的合法路径**，所以三步流程不一定是必走链路。

## 请求头契约

前端统一由 `H2(chatReq, turnstileToken, proofToken, sentinelToken, soToken, telemetry)` 生成：

```js
o['OpenAI-Sentinel-Chat-Requirements-Token'] = chatReq.token            // 单步
o['OpenAI-Sentinel-Chat-Requirements-Prepare-Token'] = chatReq.prepare_token  // 三步（与上者互斥）
o['OpenAI-Sentinel-Turnstile-Token'] = turnstileToken
o['OpenAI-Sentinel-Proof-Token']     = proofToken
o['OpenAI-Sentinel-SO-Token']        = soToken
o['OpenAI-Sentinel-Token']           = sentinelToken      // sdk.js 的 SentinelSDK.token()
o['OAI-Telemetry']                   = telemetry
```

| 头 | 来源 | 说明 |
| --- | --- | --- |
| `OpenAI-Sentinel-Chat-Requirements-Token` | 单步流程 | 本仓库当前使用 |
| `OpenAI-Sentinel-Chat-Requirements-Prepare-Token` | 三步流程 | 承载 `prepare_token`，与上者**二选一** |
| `OpenAI-Sentinel-Proof-Token` | 两步共用 | PoW 解答，前缀 `gAAAAAB` |
| `OpenAI-Sentinel-Turnstile-Token` | 两步共用 | Turnstile 解答 |
| `OpenAI-Sentinel-SO-Token` | 两步共用 | session observer token |
| `OpenAI-Sentinel-Token` | sdk.js | 独立链路，由 `SentinelSDK.token()` 提供 |
| `OAI-Telemetry` | sdk.js | 遥测，缺失时前端回退为 `'[1,null]'` |

**本仓库的头部名称已经与上游一致**（`internal/backend/backend.go:1205-1213`、
`internal/backend/responses_image.go:741-750` 使用的就是上表前四个），
不存在头部改名工作。

## 心跳

`POST /sentinel/heartbeat`（`authOption: SendIfAvailable`），**每 60 秒**一次
（`R5t = 6e4`），仅在页面可见且窗口聚焦时发送；`visibilitychange` / `focus` / `blur`
会重新调度。后端无需实现，但可作为「前端身份是否被上游认可」的旁路观测点。

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

## 对本仓库的影响

1. **无需紧急改动**：单步端点仍返回可用 `token`，现有 `ChatRequirements` 流程不受影响。
2. **头部名称无需改动**：仓库已使用上游当前的头名集合。
3. **迁移三步需要的工作量**：新增 `prepare` + `finalize` 两次 HTTP，加上一个
   **PoW 同步求解**步骤（`forceSync` 语义是「本次必须解出，不能等缓存」）。
   现有 `pow.go` 的求解器可直接复用，它已经实现了同一套掩码比较。
4. **字段名差异是主要陷阱**：`prepare` 返回的是 `prepare_token`，不是 `token`。
   直接复用现有解析逻辑会把 `token` 取成空串。最终 token 要读 `finalize` 的响应。
5. **`turnstile.required=true` 却拿不到 `dx` 时必须报错**：前端对这种情况是抛异常而非静默降级，
   后端照抄这个行为比「跳过 Turnstile」更安全 —— 静默降级只会换来一个更可疑的请求。
6. **PoW 难度语义未变**：`difficulty` 仍是十六进制掩码字符串（如 `06bf7d`），
   与现有 `pow.go` 的处理方式一致。

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

- ~~`finalize` 成功响应中最终 token 的准确字段集~~ → **2026-10-09 已实抓**：
  `{persona, token, expire_after: 540, expire_at}`，与单步端点同构；
- `finalize` 最终 token 的有效期（实抓 `expire_after` 为 540 秒，但 `expire_at` 与 `expire_after`
  在多次抓包中并不总是自洽，暂按 540 秒保守处理）；
- 三步流程相比单步是否真的提升通过率（需要 A/B 实测，目前无证据支持迁移的收益；
  2026-10-09 实测单步端点仍然可用，因此**迁移不是兼容性修复**）；
- `__skip_chatreq_finalize` 的赋值来源（服务端下发 or 分流），决定三步是否为必走链路。

## 未解决：turnstile / so 载荷格式已换代（2026-10-09）

本次抓包暴露出一个**比三步迁移更严重**的问题：仓库的 `solveTurnstileToken` 已经解不了当前上游数据。

- 上游 `turnstile.dx` / `so.collector_dx` / `so.snapshot_dx` 三者是**同一种编码**：
  base64 → 用请求体里的 `p`（`gAAAAAC...` requirements token）做循环 XOR → 得到 JSON 指令数组；
- 但新格式的**操作码是浮点数**（`40.25`、`98.68`、`37.57`、`20.42`、`26.38` 等），
  仓库 `process` 表只登记了整数 `1..24`，`turnstileKey()` 又把浮点截断成整数 → 全部落空，返回空串；
- 用 `p` 解出的数组里，`pos0` 混着浮点与整数，说明**指令槽位含义也变了**
  （旧格式操作码固定在 `ins[0]`），不能只补几个 opcode 了事；
- 浏览器最终发到 `OpenAI-Sentinel-Turnstile-Token` / `OpenAI-Sentinel-SO-Token` 的值，
  **不是**对 `dx` 做 `p`-XOR 的结果（实测四种组合都对不上），说明还有一层编码/变换；
- `so_token` 也不是响应字段：单步与 prepare 响应里只有 `so: {required, collector_dx, snapshot_dx}`，
  仓库读的 `data["so_token"]` 恒为空，`SOToken` 从来没被真正填过。

结论：这块**必须 hook 真实 JS**（`jshook` MCP，定位 bundle 里的 VM 解释器与编码函数）才能重写，
仅靠抓包无法还原。在还原之前，任何依赖 turnstile/so token 的链路都只能靠上游「不校验」兜底。
