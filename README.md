# chat-system 即时聊天系统

一个功能完整的即时通讯（IM）系统：Go 后端（REST API + 可水平扩展的 WebSocket 网关）+ Vue 3 前端 + PostgreSQL + Redis。

## 功能特性

- 用户注册 / 登录（用户名、邮箱、手机号三选一），RSA 非对称签名 JWT（access 15 分钟 + refresh 7 天，自动刷新与全端吊销）
- 单聊 / 群聊，会话列表（未读数、最后一条消息）
- 实时消息收发（WebSocket）：文本、图片、文件、引用回复
- 消息可靠性：`client_msg_id` 幂等去重、会话内有序序号（seq）、离线消息补拉（最多 500 条）、断线重连增量同步
- 已读回执、消息撤回、输入中状态（typing）
- 附件上传 / 下载（本地对象存储）
- 敏感词过滤（替换 / 拦截策略）、用户举报、管理后台（封禁用户、审计日志）
- 在线状态（presence）、登录接口滑动窗口限流
- 多网关横向扩展：跨节点消息投递基于 Redis Pub/Sub

## 架构

```
                        ┌─────────────┐
  浏览器 (Vue3) ──HTTP──▶│  api :8080  │──┐
  (frontend)            └─────────────┘  │    ┌────────────┐
       │                                 ├──▶ │ PostgreSQL │（用户/会话/消息/离线消息/审计）
       │ WS                   ┌──────────┴┐  └────────────┘
       └──────────────▶ gateway :8081 ... │
                        │ (可水平扩展)  │──▶ ┌────────────┐
                        └──────────┬┘      │   Redis    │（Pub/Sub 广播 /
                                   └──────▶└────────────┘  在线状态 / seq / 限流）
```

- **api**（`backend/cmd/api`）：无状态 HTTP REST 服务 —— 认证、会话管理、历史消息、附件、举报、管理后台。启动时自动执行数据库迁移。
- **gateway**（`backend/cmd/gateway`）：WebSocket 长连接网关 —— 消息收发、在线状态、离线补投。可起多个实例，经 Redis 通道 `chat.broadcast` 互相投递消息。
- **frontend**（`frontend/`）：Vue 3 + Vite + Pinia + axios，axios 拦截器自动刷新 token，WS 客户端支持心跳、断线退避重连、ACK 关联。

## 快速开始（Docker，推荐）

前置：Docker / Docker Compose。

```bash
# 1. 生成 JWT 密钥（首次必需，密钥不入库）
mkdir -p secrets
openssl genrsa -out secrets/jwt_rsa.pem 2048
openssl rsa -in secrets/jwt_rsa.pem -pubout -out secrets/jwt_rsa.pub.pem

# 2. 一键启动 postgres + redis + api + gateway×2
docker compose up -d --build

# 3. 启动前端开发服务器
cd frontend && npm install && npm run dev
```

打开 http://localhost:5173 ，管理员账号 `admin / admin123456`。

## 无 Docker 环境（Windows 原生）

后端为纯 Go 编译产物，依赖只有 PostgreSQL 和 Redis，均可使用便携版：

```bash
# 编译后端
cd backend && go build -o bin/api.exe ./cmd/api && go build -o bin/gateway.exe ./cmd/gateway

# 启动 PostgreSQL（创建用户/数据库 chat/chat）与 Redis 后，在项目根目录：
ADMIN_USERNAME=admin ADMIN_PASSWORD=admin123456 ./backend/bin/api.exe   # :8080
./backend/bin/gateway.exe                                                # :8081
```

配置全部通过环境变量注入，默认值见 `backend/internal/config/config.go`（如 `DATABASE_URL`、`REDIS_ADDR`、`JWT_PRIVATE_KEY` 等）。

## 目录结构

```
├── docker-compose.yml        # 编排 postgres + redis + api + gateway×2
├── secrets/                  # JWT RSA 密钥（不入库，需自行生成）
├── backend/
│   ├── cmd/api/              # HTTP REST 服务
│   ├── cmd/gateway/          # WebSocket 网关
│   └── internal/
│       ├── message/          # 消息核心服务（发送链路、幂等、扇出）
│       ├── ws/               # WS 帧协议、连接 Hub、跨节点广播
│       ├── conversation/     # 会话仓储（单聊去重、已读、离线消息）
│       ├── user/  model/     # 用户仓储、领域模型
│       ├── store/            # DB/Redis 连接 + 内嵌 SQL 迁移
│       ├── auth/  middleware/# RSA JWT、密码哈希；鉴权/CORS/限流
│       ├── bus/  presence/   # Redis Pub/Sub；在线状态
│       ├── moderation/ audit/# 敏感词引擎；异步审计日志
│       └── storage/ httpx/ config/
└── frontend/src/             # api/（REST 封装）、ws/（WS 客户端）、stores/、views/
```

## API 与协议速览

- REST：`POST /api/v1/auth/register|login|refresh`、`GET /users/me`、`GET|POST /conversations...`、`GET /conversations/:id/messages`、`POST /attachments`、管理端 `/api/v1/admin/*`
- WS 帧（JSON `{t, id, ts, data}`）：客户端 → `message.send` / `message.read` / `message.recall` / `sync.pull` / `typing` / `ping`；服务端 → `message.new` / `message.ack` / `offline.batch` / `presence` 等，详见 `backend/internal/ws/protocol.go`

## License

MIT
