package handler

import (
	"time"

	"github.com/example/chat/internal/middleware"
	"github.com/example/chat/internal/model"
	"github.com/gin-gonic/gin"
)

// RegisterRoutes 集中完成 REST API 的全部路由注册与中间件挂载。
// 路由表与拆分前的 cmd/api/main.go 完全一致：
//
//	POST   /api/v1/auth/register|login|refresh   （按 IP 限流：30 次/分钟）
//	POST   /api/v1/auth/logout                   （需登录）
//	GET    /api/v1/users/me                      （需登录）
//	GET    /api/v1/users/:id                     （需登录）
//	PATCH  /api/v1/users/me                      （需登录）
//	GET    /api/v1/conversations                 （需登录）
//	POST   /api/v1/conversations/direct          （需登录）
//	POST   /api/v1/conversations/group           （需登录）
//	POST   /api/v1/conversations/:id/members     （需登录，群主/管理员）
//	DELETE /api/v1/conversations/:id/members/:uid（需登录，群主/管理员）
//	GET    /api/v1/conversations/:id/messages    （需登录，会话成员）
//	POST   /api/v1/conversations/:id/read        （需登录）
//	POST   /api/v1/attachments                   （需登录）
//	GET    /api/v1/attachments/file/*key         （需登录，支持 ?token= 查询参数）
//	GET    /api/v1/attachments/raw/*key          （需登录，支持 ?token= 查询参数）
//	POST   /api/v1/reports                       （需登录）
//	GET    /api/v1/admin/users                   （管理员）
//	POST   /api/v1/admin/users/:id/ban           （管理员）
//	GET    /api/v1/admin/audit                   （管理员）
func RegisterRoutes(r *gin.Engine, d *Deps) {
	v1 := r.Group("/api/v1")

	// ---------- Auth ----------
	authGroup := v1.Group("/auth")
	// 认证接口按客户端 IP 限流（每分钟 30 次），缓解密码爆破与注册刷量
	authGroup.Use(middleware.RateLimit(d.RDB, "auth", time.Minute, 30, middleware.KeyByIP))
	{
		authGroup.POST("/register", d.register)
		authGroup.POST("/login", d.login)
		authGroup.POST("/refresh", d.refresh)
	}

	// ---------- 需要鉴权的路由 ----------
	authed := v1.Group("")
	authed.Use(middleware.Auth(d.JWT, d.Users))

	authed.POST("/auth/logout", d.logout)

	authed.GET("/users/me", d.usersMe)
	authed.GET("/users/:id", d.userByID)
	authed.PATCH("/users/me", d.updateProfile)

	// ---------- 会话 ----------
	authed.GET("/conversations", d.listConversations)
	authed.POST("/conversations/direct", d.createDirect)
	authed.POST("/conversations/group", d.createGroup)
	authed.POST("/conversations/:id/members", d.addMembers)
	authed.DELETE("/conversations/:id/members/:uid", d.removeMember)

	// ---------- 历史消息与已读 ----------
	authed.GET("/conversations/:id/messages", d.listMessages)
	authed.POST("/conversations/:id/read", d.markRead)

	// ---------- 附件上传 ----------
	authed.POST("/attachments", d.uploadAttachment)

	// 附件读取（流式，带 Content-Type 探测）。
	// 这两个路由挂在根 Engine 上（不在 authed 组内），因为浏览器 <img>/<a> 标签
	// 引用附件时无法携带 Authorization 头，需要 AuthWithQueryToken 兼容 ?token=
	// 查询参数的鉴权方式；验签逻辑与 middleware.Auth 完全一致，知道 key 但未登录
	// 的请求一律 401，不再允许匿名下载。
	dlAuth := middleware.AuthWithQueryToken(d.JWT, d.Users)
	r.GET("/api/v1/attachments/file/*key", dlAuth, d.downloadFile)
	r.GET("/api/v1/attachments/raw/*key", dlAuth, d.downloadRaw)

	// ---------- 举报 ----------
	authed.POST("/reports", d.createReport)

	// ---------- 管理后台 ----------
	// 所有 /admin 接口要求管理员及以上角色（RequireRole 中间件校验）
	admin := authed.Group("/admin")
	admin.Use(middleware.RequireRole(model.RoleAdmin))
	{
		admin.GET("/users", d.adminListUsers)
		admin.POST("/users/:id/ban", d.adminBanUser)
		admin.GET("/audit", d.adminListAudit)
	}
}
