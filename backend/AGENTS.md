# 后端工程约定

## 并发代码编写约定

Go 对并发没有"声明式标注"：`go` 关键字、channel、`sync` 原语本身就是声明，编译器/运行时天然识别。但**共享内存的并发安全编译器不会帮你检查**，因此本项目约定：并发代码必须同时满足「注释交代清楚 + 工具检测通过」两条，缺一不可提交。

### 一、注释必须交代的四件事

1. **为什么需要并发结构**——一句话说明动机，而不是复述代码。
   范例：`internal/ws/conn.go:28`（"下行数据统一经 sendCh 缓冲……gorilla/websocket 不允许并发写"）。
2. **锁保护哪些字段**——锁与被保护字段相邻书写，注释说明保护关系与锁策略（读写锁/互斥锁、为何分桶）。
   范例：`internal/ws/hub.go:21-28`（分桶注释）、`internal/moderation/engine.go:26-33`（"读写经 mu 保护，支持运行期热更新"）。
3. **协程的生命周期**——谁启动、谁负责让它退出、退出信号是什么（防协程泄漏）。
   范例：`internal/ws/conn.go:144-145`（"sendCh 关闭或写失败即退出"）。
4. **降级/非阻塞策略的理由**——凡是 `select` + `default`、丢弃、熔断写法，必须注明"宁可丢/断，也不阻塞谁"。
   范例：`internal/audit/audit.go:38-47`（审计缓冲满丢弃，绝不阻塞业务请求）。

### 二、共享状态三选一，不允许裸读写

| 手段 | 适用场景 | 项目实例 |
|---|---|---|
| channel 传递所有权 | 跨协程移交数据/任务 | `Conn.sendCh`（多生产者 → 单写泵）、`audit.Logger.ch` |
| 互斥锁/读写锁保护 | 高频并发读、偶发写的共享表 | `Hub` 分桶注册表、`moderation.Engine.words` |
| `atomic` 原子值 | 简单计数/标志位 | `Hub.connCount`（atomic.Int64）、`Conn.closed`（atomic.Bool） |

只执行一次的并发操作用 `sync.Once` 并注明原因（如 `Conn.closeOnce`：sendCh 只能 close 一回）。

### 三、锁纪律

- **锁范围最小化**：锁内不做网络/数据库调用，不调用外部回调。
- 需要遍历推送时，**锁内拷贝快照、放锁再执行**（`hub.go:184-209` 的 `PushToUser` 是范本），防止慢操作阻塞注册/注销，也防止回调触发死锁。
- 热路径上的全局锁优先考虑分桶/原子计数降级竞争（`hub.go:18-25`）。

### 四、channel 纪律

- **谁发送谁关闭，且只关一次**；接收方永不关闭（`Close` 里 `closeOnce.Do` + `close(c.sendCh)`）。
- 有缓冲 channel 写明容量与满后的行为（断连/丢弃/降级），见 `conn.go:14-25` 的参数注释块。

### 五、提交前验收（必跑）

```bash
go vet ./...        # 静态检查：锁拷贝、WaitGroup 误用等
go test -race ./... # 数据竞争检测：运行时抓无保护共享读写
```

`-race` 是并发代码的底线保障：编译器发现不了的竞态，靠它在测试期暴露。

## 现有并发代码范例索引

| 模式 | 位置 |
|---|---|
| 每连接读写双泵 + channel 串行化写 | `internal/ws/conn.go`、`internal/ws/handler.go:132-148` |
| 分桶读写锁 + 原子计数 + 快照推送 | `internal/ws/hub.go` |
| Redis Pub/Sub 订阅协程（跨节点扇出） | `internal/bus/bus.go:47-64` |
| 有缓冲 channel + 后台攒批协程 | `internal/audit/audit.go` |
| 读写锁保护的热更新缓存 | `internal/moderation/engine.go` |
| goroutine 起服务 + 信号 channel 优雅停机 | `cmd/api/main.go:100-117`、`cmd/gateway/main.go:117-138` |
