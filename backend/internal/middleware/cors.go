package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// CORS 按 Origin 白名单放行浏览器跨域请求（前端开发服务器与生产域名）。
// 仅对命中的 Origin 回写 CORS 响应头，未命中则不带 CORS 头、浏览器自然拦截；
// OPTIONS 预检请求直接以 204 结束，不进入业务 handler。
func CORS(allowed []string) gin.HandlerFunc {
	set := make(map[string]struct{}, len(allowed))
	for _, o := range allowed {
		set[strings.TrimSpace(o)] = struct{}{}
	}
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" {
			if _, ok := set[origin]; ok {
				c.Header("Access-Control-Allow-Origin", origin)
				c.Header("Access-Control-Allow-Credentials", "true")
				c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
				c.Header("Access-Control-Allow-Headers", "Authorization,Content-Type,X-Requested-With")
				c.Header("Access-Control-Max-Age", "600")
			}
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// OriginAllowed 供 WebSocket 网关（cmd/gateway）复用同一套 Origin 白名单，
// 因为 WS 握手不经过 Gin 中间件链，需要单独校验。
func OriginAllowed(allowed []string, origin string) bool {
	for _, o := range allowed {
		if strings.EqualFold(strings.TrimSpace(o), origin) {
			return true
		}
	}
	return false
}
