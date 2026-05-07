# ChatGPT 内容类型枚举

> 从 minified JS 逆向还原
> 标记: ✅ 已抓取验证 | 🔶 从压缩 JS 推测

## ⚠️ 重要区分

本文件记录的是**前端渲染类型系统**（`zo` 枚举），与 API 的 `content_type` discriminator 是**不同的东西**：

| 概念 | 所在层 | 用途 | 示例值 |
|---|---|---|---|
| **`zo` 枚举** | 前端 React 渲染 | 决定用哪个 React 组件渲染消息卡片 | `zo.t2uay3k` → `WY` (ImageGenCard) |
| **API `content_type`** | 后端 Pydantic | 请求/响应中消息内容的 discriminator | `"text"`, `"multimodal_text"`, `"image_asset_pointer"`, `"code"` |
| **SSE `type` 字段** | SSE 事件顶层 | 标识 SSE 事件类型 | `"title_generation"`, `"message_marker"`, `"message_stream_complete"` |
| **`recipient` 字段** | 消息对象 | 指定消息的目标组件 | `"t2uay3k.sj1i4kz"` (→ ImageGenCard) |

**关键事实**: `zo.t2uay3k` 对应消息的 `recipient: "t2uay3k.sj1i4kz"` 和 `author.name: "t2uay3k.sj1i4kz"`，它不是 SSE 事件的 `type` 字段。API 请求/响应中的 `content_type` 永远使用小写字符串如 `"text"`, `"multimodal_text"`, `"code"` 等，不会出现 `zo.t2uay3k`。

## 一、前端渲染类型枚举 (`zo`) 🔶

| 枚举值 (zo.xxx) | 组件 | 状态 | 说明 |
|---|---|---|---|
| `zo.Text` | (直接渲染) | 活跃 | 普通文本消息 |
| `zo.t2uay3k` | `WY` (ImageGenCard) | **活跃** | **gpt-image-2 当前生图类型** |
| `zo.t2uay3k_c` | — | 活跃 | t2uay3k 变体 (可能是完成态) |
| `zo.oiw209h` | `GY` (LegacyImageCard) | 活跃 | 旧版生图类型 (可能是 DALL-E 过渡) |
| `zo.Dalle` | `WY` (DalleCard) | 保留 | 原始 DALL-E 生图 |
| `zo.n7jupd_image_gen` | `null` (不渲染) | 保留 | 内部生图占位组件 |
| `zo.n7jupd_n` | `dS` | 活跃 | n7jupd 特殊类型 |
| `zo.kaur1br5` | `ZY` (AgentCard) | **活跃** | KAUR1BR5 Agent 模式 |
| `zo.DragonfruitCoT` | 特殊处理 | 活跃 | Dragonfruit Agent 思维链 |
| `zo.Browsing` | browsing 组件 | 活跃 | 搜索/浏览 |
| `zo.RetrievalBrowsing` | browsing 组件 | 活跃 | 检索式浏览 |
| `zo.Glaux` | `qY` (GlauxCard) | 活跃 | Glaux Agent 模式 |
| `zo.GizmoEditor` | `BY` | 活跃 | Gizmo 编辑器 |
| `zo.SearchResult` | `$Y` | 活跃 | 搜索结果 |
| `zo.Mochi` | `null` | **已废弃** | 返回 null |
| `zo.CoTSearchTool` | `null` | **已废弃** | 返回 null |
| `zo.SuperWidget` | `null` | **已废弃** | 返回 null |
| `zo.e1ld0dvz` | `null` | **已废弃** | 返回 null |
| `zo.is_loading_message` | loading 组件 | 活跃 | 加载状态 |

## 二、API content_type 值 (实抓验证) ✅

### 请求中使用的 content_type

| 值 | 用途 | 验证状态 |
|---|---|---|
| `"text"` | 纯文本 prompt (文本聊天 + 生图) | ✅ 两个端点均 HTTP 200 |
| `"multimodal_text"` | 多模态消息 (图片+文本) | ✅ 出现在 SSE tool 消息中 |
| `"code"` | 代码/工具调用 | ✅ 生图 SSE 中出现 |
| `"model_editable_context"` | 模型上下文初始化 | ✅ SSE 首批事件 |

### 响应中出现的 content_type

| 值 | 含义 | 验证状态 |
|---|---|---|
| `"text"` | 纯文本回复 | ✅ |
| `"multimodal_text"` | 多模态消息容器 | ✅ (包含 image_asset_pointer part) |
| `"image_asset_pointer"` | 图片资产指针 (在 parts 内) | ✅ sediment:// 协议 |
| `"code"` | 工具调用代码 `{"skipped_mainline":true}` | ✅ |
| `"model_editable_context"` | 模型可编辑上下文 | ✅ |

### 生图特有的 recipient 值

| 值 | 含义 | 验证状态 |
|---|---|---|
| `"t2uay3k.sj1i4kz"` | 图片生成工具实例 | ✅ 实抓确认 |
| `"all"` | 全局/默认接收者 | ✅ |

## 三、关键判断函数 🔶

```js
// 判断是否为异步生图消息
function qSe(e) {
  let t = e.metadata,
      n = bt(e);
  return (n === Uh.t2uay3k || n === Uh.t2uay3k_c) 
    && (!!t?.image_gen_async 
      || !!t?.image_gen_multi_stream 
      || !!t?.image_gen_task_id);
}

// 判断是否为生图内容类型
function YSe(e) {
  return e === n_.IMAGE_GEN_HALLUCINATED 
    || e?.startsWith(n_.T2UAY3K) === true;
}

// 判断是否为生图结束消息
function UJ(e) {
  return !!(e.metadata?.n7jupd_message 
    && e.channel === 'commentary' 
    && e.end_turn === true);
}
```

## 四、Author 名称枚举

| Author Name | Role | 说明 |
|---|---|---|
| `t2uay3k.sj1i4kz` | Tool | 生图工具 (IMAGEGEN_MAKE_IMAGE) — 实抓确认 ✅ |
| `n7jupd_m` | Tool | 旧版生图工具 (前端代码中仍存在) 🔶 |
| `n7jupd.metadata` | Tool | 生图元数据消息 🔶 |
| `bio` | Tool | 生物识别工具 🔶 |
| `kaur1br5.dragonfruit` | Tool | Dragonfruit Agent 🔶 |
