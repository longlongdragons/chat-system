package store

import "github.com/redis/go-redis/v9"

// NewRedis 创建 Redis 客户端。Redis 在本系统中承担两类职责：
// 缓存在线状态/会话热点数据，以及通过 chat.broadcast Pub/Sub 通道
// 在多个 gateway 节点之间转发消息，因此连接池给得相对充裕（50）。
// 不立即做连通性检查（go-redis 会在首次命令时建立连接），
// 调用方如需启动期探活需自行 Ping。
func NewRedis(addr, password string, db int) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:         addr,
		Password:     password,
		DB:           db,
		PoolSize:     50,
		MinIdleConns: 5,
	})
}
