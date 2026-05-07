# ChatGPT Images 内部代号/暗语词典

> 从 minified JS 中逆向还原 — 大部分为 🔶 推测
> 已通过实抓验证的标注 [✅ 验证]

## 模型/工具代号

| 内部代号 | 含义 | 备注 |
|---|---|---|
| `n7jupd` | 图片生成系统代号 | 出现在路由、组件、消息 metadata 中 |
| `t2uay3k.sj1i4kz` | 当前生图工具 Author/Recipient ✅ | 实抓 SSE 确认，替代了旧版 `n7jupd_m` |
| `n7jupd_m` | 旧版生图工具 Author (IMAGEGEN_MAKE_IMAGE) | 前端代码中仍存在，`n7jupd` + `m` = make |
| `n7jupd.metadata` | 生图元数据消息 Author | 点号命名风格 |
| `n7jupd_n` | 生图另一组件类型 | `n` 含义不明 |
| `n7jupd_image_gen` | 生图占位组件 | 返回 null (已被 t2uay3k 取代) |
| `n7jupd_crefs` | 生图内容引用 | content references |
| `n7jupd_button_type` | 生图消息按钮类型 | 操作按钮枚举 |
| `n7jupd_message` | 生图消息标记 | metadata 字段 |
| `n7jupdTabs` | 生图标签页 | 消息中的 tab 切换 |
| `t2uay3k` / `T2UAY3K` | 当前图片生成内容类型 ✅ | 前端渲染类型 + SSE recipient |
| `oiw209h` | 旧版图片内容类型 | 可能是 DALL-E 过渡版 |
| `kaur1br5` / `KAUR1BR5` | Agent 模式代号 | 独立于生图 |
| `Dragonfruit` / `dragonfruit` | 智能体任务系统 | Agent task system |
| `Ghostrider` / `ghostrider` | 生图流式渲染引擎 | SSE streaming renderer |
| `e1ld0dvz` | 已废弃类型 | 返回 null |
| `Glaux` | 新的 Agent/对话模式 | 带特殊渲染 |

## 功能系统代号

| 内部代号 | 含义 |
|---|---|
| `image_gen_async` | 异步生图模式 |
| `image_gen_multi_stream` | 多流并发生图 |
| `image_gen_task_id` | 异步任务追踪 ID |
| `IMAGE_GEN_HALLUCINATED` | 假图/幻觉图片标记 |
| `image_gen_rate_limit` | 生图速率限制 |
| `imagegen_watermark_download_carryover` | 水印下载跨页面传递 (sessionStorage) |
| `watermarked_asset_pointer` | 水印版图片资产指针 |
| `Paragen` / `force_paragen` | 并行生成模式 (A/B 对比) |
| `Gizmo` | 自定义 GPTs 系统 |
| `GizmoInteraction` | Gizmo 交互模式 |
| `Calpico` | 群聊系统代号 |
| `Mochi` | 已废弃功能 |
| `Wham` | 另一个功能代号 |
| `Odyssey` | 另一个功能代号 |

## 路由/URL 代号

| 路由 | 说明 |
|---|---|
| `v/:n7jupdId` | 生图结果查看页 |
| `c/:conversationId/v/:n7jupdId` | 对话内的生图结果 |

## 存储/缓存 Key

| Key | 用途 | 存储位置 |
|---|---|---|
| `imagegen_watermark_download_carryover` | 水印下载跨页面传递 | sessionStorage |
| 内容类型: `ImageAssetPointer` | 图片资产类型标识 | 全局常量 |

## 消息 Channel

| Channel | 用途 |
|---|---|
| `commentary` | 生图旁白/注释消息 |
| (默认) | 普通对话消息 |

## 资产生命周期

```
1. SSE 返回 asset_pointer (字符串哈希)
2. 客户端调用 se.fetch({ asset, conversationId }) 
   → 通过 asset_pointer 换取实际图片 URL
3. 水印版通过 watermarked_asset_pointer 单独获取
4. 下载时若需水印, 从 sessionStorage 读取 carryover 数据
```
