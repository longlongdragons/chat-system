# 性能与稳定性：现状结论与演进触发条件

> 结论先行：本项目（万级在线以内）的瓶颈不在架构而在工程细节。本文档说明现有设计的依据、已做的护栏，以及什么量级才需要引入更重的方案。

## 一、现有设计的性能判断

| 维度 | 现状 | 判断 |
|---|---|---|
| 消息落库 | 单条同步 INSERT，pgx 连接池 MaxConns=40 | 单聊/群聊 QPS 在数百级时完全够用；同步落库换来强一致与简单幂等，值得 |
| seq 分配 | Redis INCR，首次用 DB max_seq 校准，Redis 故障降级 DB max_seq+1 | O(1) 且有降级，合格 |
| 跨节点投递 | Redis Pub/Sub 单通道 `chat.broadcast` 全量广播 | 每个网关收到全部广播再本地筛选。网关 ≤ 10 实例时带宽浪费可忽略；>10 实例考虑分片（见下） |
| 在线状态 | Redis Hash + 90s TTL + 心跳续期 | 崩溃网关最多 90 秒假在线，可接受 |
| 历史翻页 | `(conversation_id, seq)` 唯一索引 + 倒序索引 | 深翻页也是索引范围扫描，合格 |
| 连接内存 | 每连接 2 goroutine + 256 帧缓冲 | 万连接 ≈ 数百 MB，正常；关键是慢消费者踢出保护（已有） |

## 二、本次已补齐的护栏

- **连接数上限**：`WS_MAX_CONNS`（默认 10000）、`WS_MAX_CONNS_PER_USER`（默认 10），超限拒绝注册
- **消息发送限流**：按用户 60 条/分钟（`WS_MSG_RATE_LIMIT` 可调），限流器故障自动放行
- **附件下载鉴权**：杜绝"知道 key 就能拖库"
- **presence 精准推送**：上线/离线只推共同会话成员，不再无效自推

## 三、什么时候做什么（触发条件）

| 信号 | 动作 |
|---|---|
| 消息发送 P99 延迟 > 200ms 且 DB CPU 饱和 | 消息 INSERT 改异步批量落库（攒批 100ms/500 条），ack 改"落库后补发"语义 |
| 单网关连接 > 2 万 | gateway 加实例 + 前置 L4 负载均衡（WS 按源地址散列即可，无状态） |
| 网关实例 > 10，`chat.broadcast` 流量成为负担 | 广播通道按 `userID % N` 分片，网关只订阅自己负责的分片 |
| 单库消息表 > 5 千万行 / 写入成为瓶颈 | messages 按 conversation_id hash 分表；历史冷数据归档 |
| 热点大群（单群 > 5000 人） | 扇出改异步队列；离线消息改"游标式懒加载"（不逐人写 offline_messages，上线时按 last_read_seq 直接查 messages） |
| Redis 单点风险不可接受 | Redis Sentinel/Cluster 切换，客户端仅改地址配置 |

## 四、数据库运维基线

- 定期 `ANALYZE`；开启 `pg_stat_statements` 盯慢查询（>100ms）
- 关注 `offline_messages`、`message_receipts` 的膨胀：已读清理逻辑已有，长期建议加定时任务物理清理 30 天前的回执
- 连接池：MaxConns=40 对单实例足够；api/gateway 多实例时注意 `总连接数 = 实例数 × 40 < PG max_connections`

## 五、前端性能现状与手段

- 已实现：路由懒加载、vendor 分包（vue/axios 独立 chunk）、`content-visibility: auto` 长列表渲染优化、typing 节流
- 后续（P2）：消息列表真虚拟滚动（单会话消息 > 5000 条时）、图片消息缩略图、Service Worker 缓存静态资源
