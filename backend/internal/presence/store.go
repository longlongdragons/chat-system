// Package presence 维护用户在线状态：用 Redis hash 记录每个用户在各个网关节点上
// 的连接数（同一用户可能多端登录、存在多条 WebSocket 连接），配合 TTL 与心跳
// 续期，实现网关节点崩溃后在线状态的自动清理。
package presence

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// presenceTTL 是在线状态键的过期时间。网关在连接存活期间通过心跳不断续期；
// 若网关节点崩溃来不及注销，其连接数会随 key 过期被自动清除，避免残留"假在线"。
const presenceTTL = 90 * time.Second

// Store 是在线状态存储，封装对 Redis 的读写。
type Store struct {
	rdb *redis.Client
}

// NewStore 基于给定的 Redis 客户端创建在线状态存储。
func NewStore(rdb *redis.Client) *Store { return &Store{rdb: rdb} }

// key 返回指定用户的在线状态 hash 键，形如 presence:user:<uid>。
func key(userID int64) string { return fmt.Sprintf("presence:user:%d", userID) }

// Add 在用户上线（建立 WebSocket 连接）时把对应网关节点 serverID 的连接计数 +1，
// 并顺手刷新 TTL；返回该节点上当前的连接数。
// 自增与续期放在同一事务管线中，避免"计数已加但 TTL 未刷新"的中间态。
func (s *Store) Add(ctx context.Context, userID int64, serverID string) (int64, error) {
	k := key(userID)
	pipe := s.rdb.TxPipeline()
	incr := pipe.HIncrBy(ctx, k, serverID, 1)
	pipe.Expire(ctx, k, presenceTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, err
	}
	return incr.Val(), nil
}

// Remove 在用户断开连接时把对应节点的计数 -1；计数归零时直接删除该 hash 字段，
// 防止 0 值字段残留导致统计偏差，返回该节点剩余的连接数。
func (s *Store) Remove(ctx context.Context, userID int64, serverID string) (int64, error) {
	k := key(userID)
	n, err := s.rdb.HIncrBy(ctx, k, serverID, -1).Result()
	if err != nil {
		return 0, err
	}
	if n <= 0 {
		s.rdb.HDel(ctx, k, serverID)
		n = 0
	}
	return n, nil
}

// Total 汇总用户在所有网关节点上的连接数之和，作为跨节点在线判断的依据。
func (s *Store) Total(ctx context.Context, userID int64) (int64, error) {
	vals, err := s.rdb.HVals(ctx, key(userID)).Result()
	if err != nil {
		return 0, err
	}
	var total int64
	for _, v := range vals {
		var n int64
		fmt.Sscanf(v, "%d", &n)
		if n > 0 {
			total += n
		}
	}
	return total, nil
}

// IsOnline 判断用户当前是否存在任意一条在线连接。
func (s *Store) IsOnline(ctx context.Context, userID int64) (bool, error) {
	n, err := s.Total(ctx, userID)
	return n > 0, err
}

// Heartbeat 刷新在线状态键的 TTL，由网关按心跳周期调用，为活跃连接续期。
func (s *Store) Heartbeat(ctx context.Context, userID int64) {
	s.rdb.Expire(ctx, key(userID), presenceTTL)
}
