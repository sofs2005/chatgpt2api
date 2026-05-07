# ChatGPT Images 关键函数映射表

> 从 minified JS 逆向还原 — 所有映射均为 🔶 推测
> 已通过实抓验证的 API 行为标注 [✅ 验证]

## 一、请求链路 (Composer → API)

| 混淆名 | 所在文件 | 推测功能 |
|---|---|---|
| `We()` | `images._index-*.js` | 图片 App Composer 主组件 |
| `fe()` | `18432d2f-*.js` | 请求完成入口 (带 callsiteId) |
| `B()` | `images._index-*.js` | 同上，images app 专用包装 |
| `Se()` | `images._index-*.js` | Composer prompt 参数准备 |
| `_e()` | `images._index-*.js` | 图片特定请求参数构建 |
| `j()` | `images._index-*.js` | Composer 提交处理 (含埋点) |
| `T()` | `images._index-*.js` | 未登录认证流程处理 |
| `OV()` | `8b34dbc2-*.js` | **核心请求完成函数** — 内部调用 prepare + generate API ✅ |
| `mp()` | `8b34dbc2-*.js` | API 请求体构建 — 生成 `parent_message_id`, `messages`, model 等字段 ✅ |
| `Wc.safePost()` | `8b34dbc2-*.js` | HTTP POST 客户端 — 携带所有 OAI/Sentinel headers ✅ |
| `Wc.safeGet()` | `8b34dbc2-*.js` | HTTP GET 客户端 |

## 二、图片选择与上传

| 混淆名 | 所在文件 | 功能 |
|---|---|---|
| `A_e()` | `8b34dbc2-*.js` | 图片选择主流程 |
| `D_e()` | `8b34dbc2-*.js` | 图片文件下载 |
| `O_e()` | `8b34dbc2-*.js` | 图片尺寸获取 |
| `k_e()` | `8b34dbc2-*.js` | 创建生图请求 (携带图片) |
| `wq()` | `8b34dbc2-*.js` | 图片搜索/目标回复 |
| `C_e()` | `8b34dbc2-*.js` | 图片选择描述文本 |
| `kj()` | `8b34dbc2-*.js` | 获取图片 prompt 文本 |
| `T_e()` | `8b34dbc2-*.js` | 多模态消息构建 |

## 三、图片生成消息渲染 (t2uay3k)

| 混淆名 | 所在文件 | 功能 |
|---|---|---|
| `WY` | `8b34dbc2-*.js` | t2uay3k / Dalle 图片卡片组件 |
| `GY` | `8b34dbc2-*.js` | oiw209h 旧版图片卡片 |
| `EM()` | `8b34dbc2-*.js` | t2uay3k 消息内容提取 |
| `QA()` | `8b34dbc2-*.js` | 消息 ID 提取 |
| `mI()` | `8b34dbc2-*.js` | 消息分组处理 |
| `Mu()` | `8b34dbc2-*.js` | 图片资产指针收集 |

## 四、判断/检测函数

| 混淆名 | 所在文件 | 功能 |
|---|---|---|
| `qSe()` | `8b34dbc2-*.js` | 判断是否异步生图消息 |
| `YSe()` | `8b34dbc2-*.js` | 判断是否为生图内容类型 |
| `JSe()` | `8b34dbc2-*.js` | 获取消息 ID |
| `XSe()` | `8b34dbc2-*.js` | 判断是否为 oiw209h 类型 |
| `ZSe()` | `8b34dbc2-*.js` | 判断 Author 名称 |
| `UJ()` | `8b34dbc2-*.js` | 判断是否为生图结束消息 |
| `KSe()` | `8b34dbc2-*.js` | 判断 type === -1 |
| `bt()` | `8b34dbc2-*.js` | 获取消息 Author 类型 |

## 五、对话/路由管理

| 混淆名 | 所在文件 | 功能 |
|---|---|---|
| `Uv` | `8b34dbc2-*.js` | 对话树管理 (ConversationTree) |
| `bi()` | `8b34dbc2-*.js` | 对话状态更新 |
| `yc()` | `8b34dbc2-*.js` | 节点查找 |
| `gm()` | `8b34dbc2-*.js` | 获取 agent metadata 节点 |
| `kf()` | `8b34dbc2-*.js` | 获取 clientThreadId |
| `Cn()` | `8b34dbc2-*.js` | 功能开关检测 (Feature Flags) |

## 六、图片库/灯箱

| 混淆名 | 所在文件 | 功能 |
|---|---|---|
| `ne()` | `7bd2b610-*.js` | 灯箱打开处理 |
| `w()` | `1cfd0849-*.js` | 图片资产指针获取 |
| `ot()` | `503786d2-*.js` | Sora/图片操作参数 |

## 七、埋点/分析

| 混淆名 | 所在文件 | 功能 |
|---|---|---|
| `b` | `images._index-*.js` | Statsig 埋点 |
| `d()` | `5ff32e89-*.js` | 图片/视频生成事件 |
| `C()` | `5ff32e89-*.js` | 图片生成点击埋点 |
| `S()` | `5ff32e89-*.js` | Sora 应用打开埋点 |
