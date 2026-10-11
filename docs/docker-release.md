# Docker 镜像打包与发布指南

本项目已具备免 Docker 守护进程的镜像构建能力（本机 Docker 引擎不可用时也能打包），
三个镜像均为 linux/amd64：

| 镜像 | 内容 | 大小 |
|---|---|---|
| `chat-api` | Go REST 服务（ko 构建，alpine 基底，入口 `/ko-app/api`） | ~27MB |
| `chat-gateway` | Go WS 网关（ko 构建，入口 `/ko-app/gateway`） | ~19MB |
| `chat-frontend` | nginx:alpine + `frontend/dist` + SPA/反代配置（crane 组装） | ~26MB |

## 一、如何构建（复现流程）

工具链：`ko`（Go 应用镜像，免 daemon）+ `crane`（镜像层组装/推送，免 daemon），
二进制在 `tools/dl/`（也可用 `go install` 获得）：

```bash
# 后端两个镜像（backend/.ko.yaml 已配好 alpine 基底与两个构建目标）
cd backend
KO_DOCKER_REPO=docker.io/<你的账号> \
ko build --bare --tarball ../tools/dl/chat-api.tar ./cmd/api
ko build --bare --tarball ../tools/dl/chat-gateway.tar ./cmd/gateway

# 前端镜像（vite build → 层 tar → crane append 到 nginx 基底）
cd frontend && npx vite build && cd ..
mkdir -p /tmp/fe-layer/usr/share/nginx/html /tmp/fe-layer/etc/nginx/conf.d
cp -r frontend/dist/* /tmp/fe-layer/usr/share/nginx/html/
cp frontend/nginx.conf /tmp/fe-layer/etc/nginx/conf.d/default.conf
tar -czf /tmp/fe-layer.tar.gz -C /tmp/fe-layer .
crane append -b <nginx基底> -f /tmp/fe-layer.tar.gz -t chat-frontend:latest -o tools/dl/chat-frontend.tar
```

> 国内拉不动 Docker Hub 基底时，把 `alpine:3.20`/`nginx:alpine` 换成
> `m.daocloud.io/docker.io/library/alpine:3.20` 这类镜像站路径即可。

## 二、推送到 Docker Hub

```bash
# 1. 登录（用 Docker Hub 的 Access Token，不是密码：
#    hub.docker.com → Account Settings → Security → New Access Token）
crane auth login index.docker.io -u <你的账号> -p <Access Token>

# 2. 推送（ko 直接推 / crane 推 tar 包都行）
cd backend && KO_DOCKER_REPO=docker.io/<你的账号> ko build ./cmd/api ./cmd/gateway
crane push tools/dl/chat-frontend.tar docker.io/<你的账号>/chat-frontend:latest

# 已有 tar 包也可以用 crane 直接推：
crane push tools/dl/chat-api.tar     docker.io/<你的账号>/chat-api:latest
crane push tools/dl/chat-gateway.tar docker.io/<你的账号>/chat-gateway:latest
```

> 注意：当前构建机的网络访问 Docker Hub 受阻（hub.docker.com / index.docker.io 不通）。
#  tar 包可直接拷到任意网络正常的机器上执行上述 push 命令，镜像内容不变。

## 三、运行（有 Docker 的机器）

```bash
# 加载本地 tar 包（若未推送）
docker load -i chat-api.tar && docker load -i chat-gateway.tar && docker load -i chat-frontend.tar

# 或直接从 Docker Hub 拉取（推送成功后），然后：
export DOCKERHUB_USER=<你的账号>
openssl genrsa -out secrets/jwt_rsa.pem 2048
openssl rsa -in secrets/jwt_rsa.pem -pubout -out secrets/jwt_rsa.pub.pem
docker compose -f docker-compose.prod.yml up -d
```

打开 http://localhost 即可使用（前端 nginx 容器托管 80 端口，/api 与 /ws 已反代到后端）。

## 四、镜像内结构说明

- **chat-api / chat-gateway**：ko 出品 = alpine 基底 + 静态编译的 Go 二进制，
  无 shell 依赖、无多余包，镜像小、攻击面小；密钥与上传目录走挂载卷（见 compose）
- **chat-frontend**：nginx:alpine + 静态产物 + `default.conf`（SPA history 兜底、
  /api 反代 api:8080、/ws 反代 gateway:8081 含 Upgrade 头与 1h 空闲保活）
