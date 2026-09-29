package handler

import (
	"strconv"

	"github.com/example/chat/internal/httpx"
	"github.com/example/chat/internal/middleware"
	"github.com/example/chat/internal/user"
	"github.com/gin-gonic/gin"
)

// usersMe 处理 GET /api/v1/users/me：返回当前登录用户完整资料。
func (d *Deps) usersMe(c *gin.Context) {
	httpx.OK(c, middleware.CurrentUser(c))
}

// userByID 处理 GET /api/v1/users/:id：按 ID 查询用户公开资料。
func (d *Deps) userByID(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	u, err := d.Users.ByID(c.Request.Context(), id)
	if err != nil {
		httpx.NotFound(c, "user_not_found")
		return
	}
	httpx.OK(c, u)
}

// updateProfile 处理 PATCH /api/v1/users/me：部分更新当前用户资料。
func (d *Deps) updateProfile(c *gin.Context) {
	// 字段用指针类型，区分"未传该字段"与"显式置空"，支持部分更新资料
	var req struct {
		Nickname  *string `json:"nickname"`
		AvatarURL *string `json:"avatar_url"`
		Signature *string `json:"signature"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, "invalid_body", err.Error())
		return
	}
	u := middleware.CurrentUser(c)
	updated, err := d.Users.UpdateProfile(c.Request.Context(), u.ID, user.UpdateProfileInput{
		Nickname: req.Nickname, AvatarURL: req.AvatarURL, Signature: req.Signature,
	})
	if err != nil {
		httpx.ServerError(c, err.Error())
		return
	}
	httpx.OK(c, updated)
}
