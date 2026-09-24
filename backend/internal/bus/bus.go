// Package bus 是基于 Redis Pub/Sub 的消息总线，供 cmd/api 与 cmd/gateway 共享：
// 任一节点产生新消息后发布到广播通道，所有网关节点订阅该通道并投递给本节点上的
// 在线连接，从而实现多网关部署下的跨节点消息扇出。
package bus

import (
	"context"
	"sync"

	"github.com/redis/go-redis/v9"
)

const (
	// ChannelBroadcast 是全局消息广播通道：所有网关节点都订阅它，新消息经此通道投递到各个节点。
	ChannelBroadcast = "chat.broadcast"
)

// Handler 是订阅回调，payload 为发布方序列化后的消息字节。
type Handler func(payload []byte)

// Bus 抽象消息总线，便于替换底层实现或在测试中打桩。
type Bus interface {
	Publish(ctx context.Context, channel string, payload []byte) error
	Subscribe(ctx context.Context, channel string, h Handler) error
	Close() error
}

// redisBus 是 Bus 的 Redis Pub/Sub 实现；subs 登记所有活跃订阅，供 Close 统一关闭。
type redisBus struct {
	rdb    *redis.Client
	mu     sync.Mutex
	subs   []*redis.PubSub
	closed bool
}

// NewRedisBus 基于给定的 Redis 客户端创建消息总线。
func NewRedisBus(rdb *redis.Client) Bus { return &redisBus{rdb: rdb} }

// Publish 将消息发布到指定通道，Redis 会把它扇出给所有订阅节点。
// 注意 Pub/Sub 不持久化：离线节点收不到，离线消息需依赖数据库补拉。
func (b *redisBus) Publish(ctx context.Context, channel string, payload []byte) error {
	return b.rdb.Publish(ctx, channel, payload).Err()
}

// Subscribe 订阅指定通道，随后由独立 goroutine 持续收取消息并回调 h；
// 可多次调用以订阅多个通道。
func (b *redisBus) Subscribe(ctx context.Context, channel string, h Handler) error {
	ps := b.rdb.Subscribe(ctx, channel)
	b.mu.Lock()
	b.subs = append(b.subs, ps)
	b.mu.Unlock()

	// 等待订阅确认，避免首条消息丢失
	if _, err := ps.Receive(ctx); err != nil {
		return err
	}
	go func() {
		ch := ps.Channel()
		for msg := range ch {
			h([]byte(msg.Payload))
		}
	}()
	return nil
}

// Close 关闭全部活跃订阅，幂等（重复调用安全），用于服务优雅退出时释放连接。
func (b *redisBus) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	for _, ps := range b.subs {
		_ = ps.Close()
	}
	return nil
}
