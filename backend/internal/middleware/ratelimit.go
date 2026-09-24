// internal/middleware/ratelimit.go
package middleware

import (
	"context"
	"fmt"
	"time"

	"github.com/example/chat/internal/httpx"
	"github.com/gin-gonic/gin"
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

// RateLimit 基于 Redis ZSET 的滑动窗口限流
// keyPrefix 区分限流场景（如登录、发消息），keyFn 决定限流维度（IP 或用户）；
// keyFn 返回空串表示该请求不参与限流，直接放行。
func RateLimit(rdb *redis.Client, keyPrefix string, window time.Duration, limit int,
	keyFn func(*gin.Context) string) gin.HandlerFunc {
	return func(c *gin.Context) {
		suffix := keyFn(c)
		if suffix == "" {
			c.Next()
			return
		}
		key := fmt.Sprintf("rl:%s:%s", keyPrefix, suffix)
		now := time.Now().UnixMilli()
		// member 附加纳秒后缀，保证同一毫秒内的多个请求在 ZSET 中各占一项，计数不重不漏
		member := fmt.Sprintf("%d-%d", now, time.Now().UnixNano()%1_000_000)

		// 限流只给 200ms 预算：Redis 抖动时不拖慢正常请求的整体延迟
		ctx, cancel := context.WithTimeout(c.Request.Context(), 200*time.Millisecond)
		defer cancel()

		ok, err := slidingWindowLua.Run(ctx, rdb, []string{key},
			now, window.Milliseconds(), limit, member).Int()
		if err != nil {
			// 限流组件故障 -> 放行，避免击穿
			c.Next()
			return
		}
		if ok == 0 {
			httpx.Fail(c, 429, "rate_limited", "too many requests")
			c.Abort()
			return
		}
		c.Next()
	}
}

// KeyByIP 以客户端 IP 为限流维度，适用于登录、注册等未登录接口。
func KeyByIP(c *gin.Context) string { return c.ClientIP() }

// KeyByUser 优先以用户 ID 为限流维度（已登录接口），
// 取不到登录态时退化为按 IP，保证中间件顺序异常时仍有兜底限流。
func KeyByUser(c *gin.Context) string {
	if u := CurrentUser(c); u != nil {
		return fmt.Sprintf("u%d", u.ID)
	}
	return c.ClientIP()
}
