# 开源 IM 项目调研与本项目落地清单

> 调研时间：2026-09。筛选标准：Go 技术栈、IM/实时推送同赛道、star ≥ 1k、社区有持续认知度。
> 原则：只提炼**能直接落到本仓库**的做法，并标注哪些"重型方案"不适合本项目当前规模。

## 一、对标项目清单

### 1. tinode/chat — 功能最全的 Go IM（≈13.5k star）

https://github.com/tinode/chat

- 全功能 IM：单聊/群聊/聊天室、多端（Web/Android/iOS）、消息漫游、已读回执、附件
- 传输层 JSON over WebSocket（另有 gRPC），与本项目一致

**可参考点**

| 做法 | 本项目对应 |
|---|---|
| 每条消息在会话内分配单调递增 seq，客户端按 seq 增量同步 | 已实现（`message/service.go` nextSeq，Redis INCR + DB 校准），思路一致 |
| `{client_msg_id}` 由客户端生成做发送幂等 | 已实现且做了双兜底（预检 + 唯一索引） |
| 在线状态推送给"与我有会话关系的人"，而非全网广播 | **本次已采纳**：`ws/hub.go` presence 事件改为推共同会话成员 |
| 附件/媒体走带鉴权的下载 URL（ticket 机制） | **本次已采纳**：附件下载增加 token 校验 |

### 2. Open-IM-Server — 微服务化大型 IM（≈14k star）

https://github.com/openimsdk/open-im-server

- 微服务架构（msggateway / msgtransfer / 用户/群组各独立 RPC 服务），Kafka 削峰，支撑集群化部署

**可参考点（选择性采纳）**

| 做法 | 判断 |
|---|---|
| 网关联接层与业务逻辑层分离（gateway 只维护连接，业务走 RPC） | 本项目 api/gateway 两进程已是同构的轻量版，**方向正确，保持** |
| 消息经 MQ 异步 transfer 落库削峰 | **不采纳**：当前单消息同步落库延迟已足够低；万级 QPS 前引入 MQ 只会增加运维成本。触发条件写入 `docs/performance.md` |
| 一切皆消息（pb+websocket 统一协议） | 本项目 JSON 帧协议 `{t,id,data}` 已是同思路的简化版 |
| 在线状态/会话路由存 Redis | 已实现（presence Hash + 心跳续期） |

### 3. Terry-Mao/goim — 连接层经典教科书（≈7.4k star）

https://github.com/Terry-Mao/goim

- comet（连接层）/logic（业务层）/job（消息推送层）三段式，B 站出品

**可参考点**

| 做法 | 判断 |
|---|---|
| 应用层心跳 + 空闲连接主动清理 | 已实现（25s ping / 75s 读超时 / pong 续期 presence TTL），参数合理 |
| 每个连接一个环形缓冲（Ring buffer），慢消费者断开保护 | 已实现（sendCh 缓冲 256，满则关闭连接），思路一致 |
| bucket 分桶降低大锁竞争 | **暂不采纳**：当前 Hub 单 RWMutex 在万级连接内不是瓶颈；达到十万级连接再分桶（路线图 P2） |
| 单 key 多连接（多端）数量上限 | **本次已采纳**：Hub 增加单用户连接数与总连接数护栏 |

### 4. gotify/server — 与本项目体量最接近（≈13k star）

https://github.com/gotify/server

- Go + Gin + WebSocket 的实时推送服务，代码简洁、工程化成熟

**可参考点**

| 做法 | 判断 |
|---|---|
| WS 鉴权 token 支持 query 参数（浏览器 WS/<img> 无法带 Header） | **本次已采纳**：附件下载与 WS 握手均支持 `?token=` |
| REST 与 WS 共用一套用户/令牌模型 | 已实现（api 与 gateway 共享 RSA 密钥验签） |
| 优雅停机：先停 listener 再排空连接 | 已实现（api 与 gateway 均有 shutdown 流程） |

### 5. chatwoot/chatwoot — 前端交互参考（≈21k star，Vue）

https://github.com/chatwoot/chatwoot

- 开源客服聊天平台，会话列表 + 消息区的交互细节成熟

**可参考点（本次前端打磨采用）**：非对称圆角气泡、头像按用户着色、时间分隔条、"正在输入…"指示、未读角标、空状态引导页。

## 二、落地优先级清单

### P0（本次已实施）

1. **handler 层拆分**：`cmd/api/main.go` 689 行 → `internal/handler/` 按域拆分（工程化基本要求，所有对标项目都没有巨型 main）
2. **附件下载鉴权**（安全缺口，tinode/gotify 均鉴权）
3. **presence 事件推共同会话成员**（tinode 的关系链推送模型）
4. **消息发送限流**（gotify/goim 均有限流；本项目 HTTP 有 WS 无，补齐）
5. **连接数护栏**（goim 单 key 连接上限同款）
6. **前端 UI 体系化**（设计令牌 + chatwoot 式交互细节 + typing 指示启用）

### P1（路线图，下个迭代）

- 单元测试与 CI（所有成熟项目的标配；本项目当前 0 测试）
- Prometheus 接入 gateway `/metrics`
- 消息搜索、@提醒、群成员管理 UI

### 明确不照搬（超出当前规模）

- Kafka/MQ 削峰、微服务拆分（OpenIM）：触发条件见 `docs/performance.md`
- bucket 分桶、Ring buffer 自研（goim）：万级连接内无收益
- gRPC 双协议（tinode）：JSON 帧协议对本项目足够
