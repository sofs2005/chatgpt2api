<h1 align="center">ChatGPT2API</h1>


<p align="center">ChatGPT2API 主要是对 ChatGPT 官网相关能力进行逆向整理与封装，提供面向 ChatGPT 图片生成、图片编辑、多图组图编辑场景的 OpenAI 兼容图片 API / 代理，并集成在线创作台、号池管理、图片库、日志与设置后台、多种账号导入方式与 Docker 自托管部署能力。管理端前端基于 React + Vite + Tailwind CSS / shadcn-style 组件构建，后台框架与登录页、设置页等视觉交互参考 <a href="https://github.com/ZyphrZero/react-go-admin">ZyphrZero/react-go-admin</a>。</p>

> [!WARNING]
> 免责声明：
>
> 本项目涉及对 ChatGPT 官网文本生成、图片生成与图片编辑等相关接口的逆向研究，仅供个人学习、技术研究与非商业性技术交流使用。
>
> - 严禁将本项目用于任何商业用途、盈利性使用、批量操作、自动化滥用或规模化调用。
> - 严禁将本项目用于破坏市场秩序、恶意竞争、套利倒卖、二次售卖相关服务，以及任何违反 OpenAI 服务条款或当地法律法规的行为。
> - 严禁将本项目用于生成、传播或协助生成违法、暴力、色情、未成年人相关内容，或用于诈骗、欺诈、骚扰等非法或不当用途。
> - 使用者应自行承担全部风险，包括但不限于账号被限制、临时封禁或永久封禁以及因违规使用等所导致的法律责任。
> - 使用本项目即视为你已充分理解并同意本免责声明全部内容；如因滥用、违规或违法使用造成任何后果，均由使用者自行承担。

> [!IMPORTANT]
> 本项目基于对 ChatGPT 官网相关能力的逆向研究实现，存在账号受限、临时封禁或永久封禁的风险。请勿使用你自己的重要账号、常用账号或高价值账号进行测试。

> [!CAUTION]
> 旧版本存在已知漏洞，请尽快升级到最新版本。公网部署时请尽量不要放置敏感信息，并自行做好访问控制与隔离。

## 项目介绍

ChatGPT2API 是一个 Go 后端与 Vite / React 管理端组成的自托管服务。后端负责对外提供 OpenAI 兼容图片 API、账号池调度、创作任务与日志存储；前端提供登录、创作台、号池管理、图片库、用户管理、日志和设置等后台页面。

管理端的后台框架、登录页布局、暗色模式切换动画、设置页交互和整体组件风格参考 [ZyphrZero/react-go-admin](https://github.com/ZyphrZero/react-go-admin)，并结合本项目的图片生成、账号池和自托管运维场景做了定制。

## 快速开始

当前后端运行时为 Go 单体服务，容器镜像启动 `/app/chatgpt2api` 二进制；前端在发布镜像中已编译为静态资源并由 Go 服务托管。

推送到 `main` 后，GitHub Actions 会自动发布 `ghcr.io/zyphrzero/chatgpt2api:latest`。服务器默认拉取这个镜像，不在生产机现场安装依赖和编译。

```bash
git clone git@github.com:ZyphrZero/chatgpt2api.git
cp .env.example .env
# 编辑 .env，可设置 CHATGPT2API_ADMIN_PASSWORD；未设置时首次启动会输出一次性管理员密码
docker compose up -d
```

升级：

```bash
git pull
docker compose up -d
```

如需从当前源码自建镜像：

```bash
docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build
```

如果服务器拉取 GHCR 镜像时提示 denied，请在 GitHub Packages 中把镜像设为 Public，或先执行 `docker login ghcr.io`。

本地开发或自建二进制：

```bash
go test ./...
go build -ldflags "-X chatgpt2api/internal/version.Version=1.0.0" -o chatgpt2api ./cmd/chatgpt2api
CHATGPT2API_ADMIN_PASSWORD=change_me_please ./chatgpt2api
```

### 存储后端配置

运行时配置统一写在 `.env` 中；容器部署时也可以用平台环境变量覆盖 `.env` 中的同名值。支持通过环境变量 `STORAGE_BACKEND` 切换存储方式：

- `sqlite` - 本地 SQLite 数据库（默认）
- `json` - 本地 JSON 文件
- `postgres` - 外部 PostgreSQL（需配置 `DATABASE_URL`）

示例：使用 SQLite
```env
STORAGE_BACKEND=sqlite
DATABASE_URL=sqlite:////app/data/chatgpt2api.db
```

从 JSON 文件存储迁移到 SQLite：
```bash
docker compose down
python3 scripts/migrate-json-to-sqlite.py --data-dir ./data --db ./data/chatgpt2api.db
# 编辑 .env：STORAGE_BACKEND=sqlite，DATABASE_URL=sqlite:////app/data/chatgpt2api.db
docker compose up -d
```

迁移脚本会导入 `data` 下的 JSON 文件、账号和 auth key 数据；历史 `logs.jsonl` 不迁移，新日志会在 SQLite 后端启用后重新写入数据库。如果目标库已存在，会先生成同目录 `.bak-时间戳` 备份。

示例：使用 PostgreSQL
```yaml
environment:
  - STORAGE_BACKEND=postgres
  - DATABASE_URL=postgresql://user:password@host:5432/dbname
```

## 功能

### 账号与权限

- 使用本地账号密码登录，不再使用部署时生成的 `AUTH_KEY` 作为后台登录密钥
- 首次启动会按 `CHATGPT2API_ADMIN_USERNAME` / `CHATGPT2API_ADMIN_PASSWORD` 初始化管理员；未设置密码时会生成一次性管理员密码并输出到启动日志
- 支持在设置页开启或关闭登录页账号注册入口，新注册账号默认绑定普通用户角色
- 内置 RBAC 角色层，角色统一维护菜单权限和 API 权限，用户管理页可为用户分配角色
- 支持 Linuxdo 登录与本地账号并存，登录后签发本地会话令牌，个人 API 令牌独立管理

### API 兼容能力

- 兼容 `POST /v1/images/generations` 图片生成接口
- 兼容 `POST /v1/images/edits` 图片编辑接口
- 兼容面向图片场景的 `POST /v1/chat/completions`
- 兼容面向图片场景的 `POST /v1/responses`
- 管理端异步创作任务接口统一使用 `/api/creation-tasks` 作为资源根路径，提交子接口按任务类型区分为 `image-generations`、`image-edits`、`chat-completions`
- `GET /v1/models` 返回 `gpt-image-2`、`codex-gpt-image-2`、`auto`、`gpt-5-mini`、`gpt-5-3-mini`、`gpt-5`、`gpt-5-1`、
  `gpt-5-2`、`gpt-5-3`、`gpt-5.4`、`gpt-5.5`
- 支持通过 `n` 返回多张生成结果
- 支持 Codex 中的画图接口逆向，仅 `Plus` / `Team` / `Pro` 订阅可用，模型别名为 `codex-gpt-image-2`，如有需要可自行在其他场景映射回 `gpt-image-2`，用于和官网画图区分；也就意味着同一账号会同时有官网和 Codex 两份生图额度

### 在线创作台功能

- 内置在线创作台，支持手动切换对话 / 作画模式
- 作画模式支持生成、图片编辑与多图组图编辑，图片模型为 `gpt-image-2`、`codex-gpt-image-2`、`auto`
- 对话模式保留 `auto`、`gpt-5-mini`、`gpt-5-3-mini`、`gpt-5`、`gpt-5-1`、`gpt-5-2`、`gpt-5-3`、`gpt-5.4`、`gpt-5.5` 模型选择
- 编辑模式支持参考图上传
- 前端支持多图生成交互
- 本地保存图片会话历史，支持回看、删除和清空
- 支持服务端缓存图片URL

### 号池管理功能

- 自动刷新账号邮箱、类型、额度和恢复时间
- 轮询可用账号执行图片生成与图片编辑
- 遇到 Token 失效类错误时自动剔除无效 Token
- 定时检查限流账号并自动刷新
- 支持网页端配置全局 HTTP / HTTPS / SOCKS5 / SOCKS5H 代理
- 支持搜索、筛选、批量刷新、导出、手动编辑和清理账号
- 支持四种导入方式：本地 CPA JSON 文件导入、远程 CPA 服务器导入、`sub2api` 服务器导入、`access_token` 导入
- 支持在设置页配置 `sub2api` 服务器，筛选并批量导入其中的 OpenAI OAuth 账号

### 实验性 / 规划中

- `/v1/complete` 文本补全与流式输出已实现，但仍在测试，目前会出现对话重复的问题，请谨慎测试使用
- 详细状态说明见：[功能清单](./docs/feature-status.en.md)

## Screenshots

文生图界面：

![image](assets/image.png)

编辑图：

![image](assets/image_edit.png)

Cherry Studio 中使用，支持作为绘图接口接入：

![image](assets/chery_studio.png)

号池管理：

![image](assets/account_pool.png)

New Api 接入：

![image](assets/new_api.png)

## API

所有 AI 接口都需要请求头：

```http
Authorization: Bearer <session-or-api-token>
```

管理端创作台使用异步创作任务接口提交、轮询和取消任务。这组接口不是 OpenAI 兼容接口，而是后台 UI 使用的任务资源接口：

| 方法 | 路径 | 说明 |
|:--|:--|:--|
| `GET` | `/api/creation-tasks?ids=<id1,id2>` | 按任务 ID 轮询创作任务状态 |
| `POST` | `/api/creation-tasks/image-generations` | 提交图片生成任务 |
| `POST` | `/api/creation-tasks/image-edits` | 提交图片编辑任务，使用 multipart/form-data 上传参考图 |
| `POST` | `/api/creation-tasks/chat-completions` | 提交文本/对话补全任务 |
| `POST` | `/api/creation-tasks/{id}/cancel` | 取消排队中或运行中的创作任务 |

权限系统中对应的 API 权限为 `GET /api/creation-tasks` 和 `POST /api/creation-tasks`，并按子路径生效。

<details>
<summary><code>GET /v1/models</code></summary>
<br>

返回当前暴露的模型列表。

```bash
curl http://localhost:8000/v1/models \
  -H "Authorization: Bearer <session-or-api-token>"
```

<details>
<summary>说明</summary>
<br>

| 字段   | 说明                                                                                                         |
|:-----|:-----------------------------------------------------------------------------------------------------------|
| 返回模型 | `gpt-image-2`、`codex-gpt-image-2`、`auto`、`gpt-5-mini`、`gpt-5-3-mini`、`gpt-5`、`gpt-5-1`、`gpt-5-2`、`gpt-5-3`、`gpt-5.4`、`gpt-5.5` |
| 接入场景 | 可接入 Cherry Studio、New API 等上游或客户端                                                                          |

<br>
</details>
</details>

<details>
<summary><code>POST /v1/images/generations</code></summary>
<br>

OpenAI 兼容图片生成接口，用于文生图。

```bash
curl http://localhost:8000/v1/images/generations \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <session-or-api-token>" \
  -d '{
    "model": "auto",
    "prompt": "一只漂浮在太空里的猫",
    "n": 1,
    "response_format": "b64_json"
  }'
```

<details>
<summary>字段说明</summary>
<br>

| 字段                | 说明                                                 |
|:------------------|:---------------------------------------------------|
| `model`           | 图片模型，仅支持 `gpt-image-2`、`codex-gpt-image-2`、`auto`，默认 `auto` |
| `prompt`          | 图片生成提示词                                            |
| `n`               | 生成数量，当前后端限制为 `1-4`                                 |
| `response_format` | 当前请求模型中包含该字段，默认值为 `b64_json`                       |

<br>
</details>
</details>

<details>
<summary><code>POST /v1/images/edits</code></summary>
<br>

OpenAI 兼容图片编辑接口，用于上传图片并生成编辑结果。

```bash
curl http://localhost:8000/v1/images/edits \
  -H "Authorization: Bearer <session-or-api-token>" \
  -F "model=auto" \
  -F "prompt=把这张图改成赛博朋克夜景风格" \
  -F "n=1" \
  -F "image=@./input.png"
```

<details>
<summary>字段说明</summary>
<br>

| 字段       | 说明                                  |
|:---------|:------------------------------------|
| `model`  | 图片模型，仅支持 `gpt-image-2`、`codex-gpt-image-2`、`auto`，默认 `auto` |
| `prompt` | 图片编辑提示词                             |
| `n`      | 生成数量，当前后端限制为 `1-4`                  |
| `image`  | 需要编辑的图片文件，使用 multipart/form-data 上传 |

<br>
</details>
</details>

<details>
<summary><code>POST /v1/chat/completions</code></summary>
<br>

面向图片场景的 Chat Completions 兼容接口，不是完整通用聊天代理。

```bash
curl http://localhost:8000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <session-or-api-token>" \
  -d '{
    "model": "auto",
    "messages": [
      {
        "role": "user",
        "content": "生成一张雨夜东京街头的赛博朋克猫"
      }
    ],
    "modalities": ["image"],
    "n": 1
  }'
```

<details>
<summary>字段说明</summary>
<br>

| 字段         | 说明                |
|:-----------|:------------------|
| `model`    | 图片模型，默认按图片生成场景处理  |
| `messages` | 消息数组，需要是图片相关请求内容  |
| `modalities` | 显式触发图片场景时传 `["image"]`，图片模型仍使用 `gpt-image-2`、`codex-gpt-image-2` 或 `auto` |
| `n`        | 生成数量，按当前实现解析为图片数量 |
| `stream`   | 已实现，但仍在测试         |

<br>
</details>
</details>

<details>
<summary><code>POST /v1/responses</code></summary>
<br>

面向图片生成工具调用的 Responses API 兼容接口，不是完整通用 Responses API 代理。

```bash
curl http://localhost:8000/v1/responses \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <session-or-api-token>" \
  -d '{
    "model": "gpt-image-2",
    "input": "生成一张未来感城市天际线图片",
    "tools": [
      {
        "type": "image_generation"
      }
    ]
  }'
```

<details>
<summary>字段说明</summary>
<br>

| 字段       | 说明                            |
|:---------|:------------------------------|
| `model`  | 响应中会回显该模型字段，但图片生成当前仍走图片生成兼容逻辑 |
| `input`  | 输入内容，需要能解析出图片生成提示词            |
| `tools`  | 必须包含 `image_generation` 工具请求  |
| `stream` | 已实现，但仍在测试                     |

<br>
</details>
</details>

## 社区支持

Telegram 群组：[ChatGPT2API](https://t.me/+YBR7t_CPOYBkYzU1)

学 AI , 上 L 站：[LinuxDO](https://linux.do)

## 提示词来源

内置提示词参考来源：

- [banana-prompt-quicker](https://github.com/glidea/banana-prompt-quicker)，作者：[peiyizhi](https://linux.do/u/peiyizhi)
- [awesome-gpt-image-2-prompts](https://github.com/EvoLinkAI/awesome-gpt-image-2-prompts)

## Contributors

感谢所有为本项目做出贡献的开发者：

<a href="https://github.com/basketikun/chatgpt2api/graphs/contributors">
  <img alt="Contributors" src="https://contrib.rocks/image?repo=basketikun/chatgpt2api" />
</a>

## Star History

[![Star History Chart](https://api.star-history.com/chart?repos=basketikun/chatgpt2api&type=date&legend=top-left)](https://www.star-history.com/?repos=basketikun%2Fchatgpt2api&type=date&legend=top-left)
