# 架构说明（优化后）

> 本文对应 2026-09 重构后的代码结构。调研依据见 `docs/best-practices.md`，性能判断见 `docs/performance.md`。

## 一、进程视图

```
                        ┌──────────────────┐
  浏览器 (Vue3) ─HTTP──▶│   api :8080      │  无状态 REST：认证/会话/历史/附件/管理
  (frontend)            └──────┬───────────┘
       │                       │            ┌────────────┐
       │                  ┌────┴────┐       │ PostgreSQL │  用户/会话/消息/离线/审计
       │ WS               │ 共享存储 ├──────▶└────────────┘
       └──────────▶ gateway :8081  │       ┌────────────┐
                    (可水平扩展) ───┼──────▶│   Redis    │  Pub/Sub 投递/presence/seq/限流
                                    │       └────────────┘
                        gateway :8082（实例 N，经 chat.broadcast 互联）
```

- **api**：纯 HTTP。不感知 WebSocket，不做实时推送，因此可任意多实例。
- **gateway**：纯长连接。消息落库、敏感词、扇出都在这里；实例间通过 Redis Pub/Sub 通道 `chat.broadcast` 交换下行事件（全量广播、各节点只投递本地连接）。
- 两进程**不直接通信**，共享 Postgres（数据）与 Redis（状态/投递），用同一份 RSA 公钥验签 JWT。

## 二、api 内部分层（重构后）

```
cmd/api/main.go（119 行薄装配层）
  └─ 配置加载 → 依赖构造 → store.Migrate → handler.RegisterRoutes → 优雅关停
       └─ cmd/api/bootstrap.go：ensureAdmin 管理员初始化

internal/handler/          ← 控制层：参数绑定、权限判断、响应组装，不含业务规则
  deps.go                  Deps 依赖注入（db/rdb/repos/jwt/storage/audit/cfg）
  router.go                全部 22 条路由与中间件挂载的唯一入口
  auth.go user.go conversation.go message.go attachment.go admin.go

internal/                  ← 领域与基建层（api 与 gateway 复用）
  user/ conversation/ message/   领域服务与仓储（SQL 只在仓储层）
  auth/                      RSA JWT 签发/验签、bcrypt
  middleware/                Auth（含 ?token= 变体）、RequireRole、CORS、限流中间件
  ratelimit/                 可复用滑动窗口限流器（HTTP 与 WS 共用）
  moderation/ audit/ storage/ httpx/ model/ config/
```

职责边界：**handler 只做 HTTP 语义**（解析、校验、状态码），**业务规则在 service/repo**（成员校验、幂等、seq、扇出），**SQL 只在 repo**。跨域依赖通过构造期注入，无包级全局变量。

## 三、gateway 内部结构

```
cmd/gateway/main.go        装配：db/redis/bus/presence/moderation/message.Service/ws.Hub
internal/ws/
  protocol.go              帧协议 {t,id,ts,data} 与各 payload 定义
  conn.go                  单连接读写泵：256 帧缓冲、慢消费者踢出、25s ping/75s 超时
  hub.go                   连接注册表 users[uid][connID]；连接数/单用户连接数护栏；
                           presence 精准扇出（本人 + 共同会话成员）；
                           chat.broadcast 订阅与本地投递
  handler.go               握手鉴权（?token=）、帧分发、消息发送限流（60条/分钟）
internal/message/service.go 发送链路：成员/禁言 → 敏感词 → 幂等 → seq → 落库 → 扇出
```

## 四、一条消息的时序

```
A客户端            gateway-1              Redis             PG              gateway-2/B客户端
   │ message.send      │                   │                │                    │
   │──────────────────▶│ 限流→校验→过滤→幂等 │                │                    │
   │                   │ INCR conv:seq ───▶│                │                    │
   │                   │ INSERT messages ──────────────────▶│                    │
   │                   │ 更新会话/未读 ─────────────────────▶│                    │
   │ message.ack ◀─────│                   │                │                    │
   │                   │ PUBLISH chat.broadcast ─▶│          │                    │
   │ message.new ◀─────│（A 在本机直接投递）  │              │                    │
   │                   │                   │  订阅回调 ────────────────────────▶│ message.new（B 在 gateway-2）
   │                   │ 离线成员 → INSERT offline_messages ─▶│（B 上线时补拉）   │
```

## 五、关键设计决策（与调研结论的对应）

1. **无状态双进程**（OpenIM msggateway/msgtransfer 的轻量版）：扩缩容只加进程，无服务注册中心。
2. **同步落库 + 幂等**（tinode 同款）：ack 即"已持久化"，语义简单可靠；异步批量落库列为触发式演进项。
3. **全量广播换路由简单**（goim job 的简化版）：网关 ≤10 实例时最优解；更多实例再分片。
4. **限流/鉴权/审计收口在基建层**：HTTP 与 WS 共用同一个 `ratelimit.Limiter` 与同一套验签核心，避免两处实现漂移。

## 六、核心业务流程

1. **认证**：注册/登录 → bcrypt 校验密码 → RSA 私钥签 JWT（access 15 分钟 + refresh 7 天）→ 前端 axios 拦截器自动刷新 → WS 握手用 `?token=` 同款验签（`internal/auth`、`internal/middleware`）。
2. **发消息**：见第四节时序图。链路要点——限流（60 条/分）→ 成员/禁言校验 → 敏感词过滤 → `client_msg_id` 幂等 → Redis INCR 分配会话内 seq → 落库 → ack（ack 即"已持久化"）→ 在线扇出 / 离线写 `offline_messages`。
3. **断线重连**：WS 心跳（25s ping / 75s 读超时）→ 前端指数退避重连 → `sync.pull` 按 seq 增量补齐缺口；发送幂等键保证重发不重复。
4. **在线状态（presence）**：连接注册/注销时 Redis 在线计数 ±1，归零才判彻底离线；状态变化只推"本人 + 共同会话成员"（tinode 关系链推送模型），不做全网广播。
5. **管理后台**：封禁用户（全端 token 吊销 + WS 踢下线）、敏感词热更新（RWMutex 整体换词表）、审计日志（异步攒批写库，不阻塞请求路径）。
6. **前端分层**：`views/`（视图）→ `stores/`（Pinia 状态与业务编排）→ `api/` + `ws/`（HTTP/WS 客户端封装），视图不直接发请求。

## 七、架构风格定位（FAQ）

**不是传统四层架构，也不是"Go 官方架构"（Go 官方从不规定架构）。** 它是两者的混合：

- **进程级**：IM 领域特有的**连接层 / 业务层双进程拆分**——gateway 只管长连接、api 只管 HTTP（goim comet/logic、OpenIM msggateway/msgtransfer 的轻量版），与传统单体 Web 应用思路不同。
- **代码级**：Go 社区惯例的 `cmd/` + `internal/` **按域分包**。与四层架构的对应：`handler ≈ controller`、`service ≈ service`、`repo ≈ dao`、`model 包 ≈ model`；区别是不按层建大包，而是 `message/` 一个包内自带 service + repo，聚合度更高。
- **依赖方向**：装配层（main.go）构造期注入全部依赖（`handler.Deps`），无包级全局变量——借 Clean Architecture 的依赖规则，但不引入 DDD 的聚合根/值对象仪式。
