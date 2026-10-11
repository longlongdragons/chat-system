# 前端镜像的标准 Dockerfile 路线（多阶段构建）
# 与仓库里实际使用的 ko/crane 免守护进程路线等价，二选一即可：
# 有可用 Docker 引擎时 `docker build` 走本文件；没有就走 docs/docker-release.md 的 crane 路线。

# ---- 阶段 1：构建 ----
FROM node:20-alpine AS builder
WORKDIR /app
# 只拷依赖清单，源码变动时不破坏依赖层缓存
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY . .
RUN npm run build

# ---- 阶段 2：运行 ----
# nginx 托管静态产物；default.conf 里含 SPA 兜底与 /api、/ws 反代
FROM nginx:alpine
COPY --from=builder /app/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]
