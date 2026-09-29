// Package middleware 提供 HTTP API（cmd/api）使用的 Gin 中间件：
// JWT 认证与角色鉴权、跨域（CORS）白名单、基于 Redis 的滑动窗口限流。
package middleware

import (
	"strings"

	"github.com/example/chat/internal/auth"
	"github.com/example/chat/internal/httpx"
	"github.com/example/chat/internal/model"
	"github.com/example/chat/internal/user"
	"github.com/gin-gonic/gin"
)

const (
	// Gin 上下文中存放认证结果的键，供后续 handler 通过 CurrentUser / CurrentClaims 取用
	CtxUserKey   = "ctx.user"
	CtxClaimsKey = "ctx.claims"
)

// Auth 是 REST 接口的登录态守卫：校验 Authorization 头中的访问令牌，
// 并把通过校验的用户与令牌声明放入请求上下文。
// 除签名与有效期外还做三重业务检查：必须是 access 类型令牌（防止拿 refresh
// 令牌调业务接口）、账号未被封禁、令牌版本号与用户当前版本一致
// （改密/踢下线后递增版本即可让旧令牌立即失效）。
func Auth(jwtMgr *auth.Manager, users *user.Repo) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := bearer(c)
		if raw == "" {
			httpx.Unauthorized(c, "missing_token")
			c.Abort()
			return
		}
		authenticate(c, jwtMgr, users, raw)
	}
}

// AuthWithQueryToken 与 Auth 完成完全相同的验签与业务校验，区别仅在取令牌方式：
// 优先取 Authorization: Bearer 头，头为空时回落到 ?token= 查询参数。
// 用于附件下载等被浏览器 <img>/<a> 标签直接引用的接口——这类请求无法携带
// 自定义 Header，只能把 access token 放在 URL 查询串里。
// 注意：URL 中的 token 可能进入访问日志/浏览器历史，因此仅对下载类只读接口启用。
func AuthWithQueryToken(jwtMgr *auth.Manager, users *user.Repo) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := bearer(c)
		if raw == "" {
			raw = strings.TrimSpace(c.Query("token"))
		}
		if raw == "" {
			httpx.Unauthorized(c, "missing_token")
			c.Abort()
			return
		}
		authenticate(c, jwtMgr, users, raw)
	}
}

// authenticate 是 Auth / AuthWithQueryToken 共用的验签核心：
// 依次校验签名与有效期、令牌类型（必须 access）、用户存在且未封禁、
// token_version 一致（未被吊销）；全部通过则把用户与声明写入请求上下文。
// 任一校验失败时写出对应的错误响应并 Abort，不会继续执行后续 handler。
func authenticate(c *gin.Context, jwtMgr *auth.Manager, users *user.Repo, raw string) {
	claims, err := jwtMgr.Verify(raw)
	if err != nil || claims.Type != auth.TokenTypeAccess {
		httpx.Unauthorized(c, "invalid_token")
		c.Abort()
		return
	}
	u, err := users.ByID(c.Request.Context(), claims.UserID)
	if err != nil {
		httpx.Unauthorized(c, "user_not_found")
		c.Abort()
		return
	}
	if u.Status != model.UserStatusNormal {
		httpx.Forbidden(c, "user_disabled")
		c.Abort()
		return
	}
	if u.TokenVersion != claims.TokenVer {
		httpx.Unauthorized(c, "token_revoked")
		c.Abort()
		return
	}
	c.Set(CtxUserKey, u)
	c.Set(CtxClaimsKey, claims)
	c.Next()
}

// RequireRole 要求当前登录用户的角色等级不低于 minRole（如管理后台接口）。
// 必须串在 Auth 之后使用，否则 CurrentUser 取不到用户会一律拒绝。
func RequireRole(minRole int16) gin.HandlerFunc {
	return func(c *gin.Context) {
		u := CurrentUser(c)
		if u == nil || u.Role < minRole {
			httpx.Forbidden(c, "insufficient_role")
			c.Abort()
			return
		}
		c.Next()
	}
}

// CurrentUser 从请求上下文取出已认证用户；未经过 Auth 中间件时返回 nil。
func CurrentUser(c *gin.Context) *model.User {
	v, ok := c.Get(CtxUserKey)
	if !ok {
		return nil
	}
	u, _ := v.(*model.User)
	return u
}

// CurrentClaims 从请求上下文取出令牌声明（含用户 ID、设备 ID、令牌版本）。
func CurrentClaims(c *gin.Context) *auth.Claims {
	v, ok := c.Get(CtxClaimsKey)
	if !ok {
		return nil
	}
	cl, _ := v.(*auth.Claims)
	return cl
}

// bearer 从 Authorization 头提取 Bearer 令牌，格式不符时返回空串（由调用方统一按"未提供令牌"处理）。
func bearer(c *gin.Context) string {
	h := c.GetHeader("Authorization")
	if h == "" {
		return ""
	}
	parts := strings.SplitN(h, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}
