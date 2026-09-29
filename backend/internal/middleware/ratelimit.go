// internal/middleware/ratelimit.go
package middleware

import (
	"fmt"
	"time"

	"github.com/example/chat/internal/httpx"
	"github.com/example/chat/internal/ratelimit"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// RateLimit 基于 Redis ZSET 的滑动窗口限流
// keyPrefix 区分限流场景（如登录、发消息），keyFn 决定限流维度（IP 或用户）；
// keyFn 返回空串表示该请求不参与限流，直接放行。
// 核心判定逻辑委托给 ratelimit.Limiter（与 WebSocket 消息限流共用同一实现）；
// 限流组件故障时放行，避免击穿。
func RateLimit(rdb *redis.Client, keyPrefix string, window time.Duration, limit int,
	keyFn func(*gin.Context) string) gin.HandlerFunc {
	limiter := ratelimit.New(rdb)
	return func(c *gin.Context) {
		suffix := keyFn(c)
		if suffix == "" {
			c.Next()
			return
		}
		key := fmt.Sprintf("rl:%s:%s", keyPrefix, suffix)

		ok, err := limiter.Allow(c.Request.Context(), key, limit, window)
		if err != nil {
			// 限流组件故障 -> 放行，避免击穿
			c.Next()
			return
		}
		if !ok {
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
