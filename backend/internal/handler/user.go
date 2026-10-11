package handler

import (
	"strconv"
	"strings"

	"github.com/example/chat/internal/httpx"
	"github.com/example/chat/internal/middleware"
	"github.com/example/chat/internal/model"
	"github.com/example/chat/internal/user"
	"github.com/gin-gonic/gin"
)

// usersMe 处理 GET /api/v1/users/me：返回当前登录用户完整资料。
func (d *Deps) usersMe(c *gin.Context) {
	httpx.OK(c, middleware.CurrentUser(c))
}

// searchUsers 处理 GET /api/v1/users/search?q=关键词&limit=20：
// 按用户名/昵称模糊搜索用户，供"添加联系人/拉人入群"场景发现用户，摆脱手输 ID。
// q 为空时返回空列表而非报错，对应前端清空搜索框的自然交互。
func (d *Deps) searchUsers(c *gin.Context) {
	q := strings.TrimSpace(c.Query("q"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if q == "" {
		// 返回与正常结果同构的空数组（JSON [] 而非 null），前端无需特判
		httpx.OK(c, gin.H{"users": []*model.User{}})
		return
	}
	users, err := d.Users.Search(c.Request.Context(), q, limit)
	if err != nil {
		httpx.ServerError(c, err.Error())
		return
	}
	httpx.OK(c, gin.H{"users": users})
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
