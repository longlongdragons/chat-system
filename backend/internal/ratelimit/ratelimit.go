// Package ratelimit 提供可复用的滑动窗口限流组件。
//
// 核心算法以 Redis ZSET 记录窗口内的请求时间戳，通过 Lua 脚本原子完成
// 「清理过期记录 -> 计数 -> 判定 -> 写入」全过程，避免并发请求下计数失真。
// 该组件由 middleware/ratelimit.go 中的 HTTP 限流中间件抽取而来，
// 现同时服务于 HTTP 接口限流与 WebSocket 消息发送限流两个场景。
package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// slidingWindowLua 用 Lua 脚本在 Redis 中原子完成滑动窗口限流：
// 以 ZSET 记录窗口内的请求时间戳，先剔除过期记录，再判断当前请求数是否超限。
// 用脚本保证"清理 + 计数 + 写入"一步完成，避免并发请求下计数失真。
var slidingWindowLua = redis.NewScript(`
local key    = KEYS[1]
local now    = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local limit  = tonumber(ARGV[3])
redis.call('ZREMRANGEBYSCORE', key, 0, now - window)
local count = redis.call('ZCARD', key)
if count >= limit then return 0 end
redis.call('ZADD', key, now, ARGV[4])
redis.call('PEXPIRE', key, window)
return 1
`)

// Limiter 是基于 Redis 的滑动窗口限流器，对调用方透明封装 Lua 脚本细节。
// 零依赖状态、可多 goroutine 并发使用；不同业务场景只需用不同的 key 前缀区分。
type Limiter struct {
	rdb *redis.Client
	// timeout 是单次限流判定的等待预算：限流只是辅助能力，
	// Redis 抖动时不能让它拖慢业务请求的整体延迟，超时即视为故障返回。
	timeout time.Duration
}

// New 创建限流器。判定超时预算固定为 200ms（与抽取前 HTTP 中间件的行为一致）。
func New(rdb *redis.Client) *Limiter {
	return &Limiter{rdb: rdb, timeout: 200 * time.Millisecond}
}

// Allow 判定 key 在 window 窗口内的本次请求是否放行：
// 窗口内已计数请求数 < limit 时计入本次并返回 true，否则返回 false。
// key 需调用方自带业务前缀（如 "rl:auth:1.2.3.4"、"rl:ws_msg:u42"）。
// 返回 error 表示限流组件自身故障（Redis 不可达/超时），
// 此时是否放行由调用方决定——本系统两处调用方（HTTP 中间件、WS 消息限流）
// 均采用"故障放行"策略，避免限流组件故障击穿正常业务。
func (l *Limiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	now := time.Now().UnixMilli()
	// member 附加纳秒后缀，保证同一毫秒内的多个请求在 ZSET 中各占一项，计数不重不漏
	member := fmt.Sprintf("%d-%d", now, time.Now().UnixNano()%1_000_000)

	// 限流只给固定预算：Redis 抖动时不拖慢正常请求的整体延迟
	ctx, cancel := context.WithTimeout(ctx, l.timeout)
	defer cancel()

	ok, err := slidingWindowLua.Run(ctx, l.rdb, []string{key},
		now, window.Milliseconds(), limit, member).Int()
	if err != nil {
		return false, err
	}
	return ok == 1, nil
}
