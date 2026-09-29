package handler

import (
	"net/http"
	"strings"

	"github.com/example/chat/internal/audit"
	"github.com/example/chat/internal/auth"
	"github.com/example/chat/internal/httpx"
	"github.com/example/chat/internal/middleware"
	"github.com/example/chat/internal/model"
	"github.com/example/chat/internal/user"
	"github.com/gin-gonic/gin"
)

// register 处理 POST /api/v1/auth/register：账号注册。
// 支持 username/email/phone 任一作为登录标识，注册成功即签发令牌对直接登录。
func (d *Deps) register(c *gin.Context) {
	var req struct {
		Username string `json:"username"`
		Email    string `json:"email"`
		Phone    string `json:"phone"`
		Password string `json:"password"`
		Nickname string `json:"nickname"`
		DeviceID string `json:"device_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, "invalid_body", err.Error())
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.Email = strings.TrimSpace(req.Email)
	req.Phone = strings.TrimSpace(req.Phone)
	if len(req.Password) < 6 {
		httpx.BadRequest(c, "weak_password", "password must be at least 6 characters")
		return
	}
	if req.Username == "" && req.Email == "" && req.Phone == "" {
		httpx.BadRequest(c, "missing_identity", "username/email/phone required")
		return
	}
	// 预先查重，避免唯一约束冲突时直接暴露数据库错误；
	// 真正的并发兜底仍依赖 users 表的唯一索引
	exists, err := d.Users.Exists(c.Request.Context(), req.Username, req.Email, req.Phone)
	if err != nil {
		httpx.ServerError(c, err.Error())
		return
	}
	if exists {
		httpx.Fail(c, http.StatusConflict, "already_exists", "user already exists")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		httpx.ServerError(c, err.Error())
		return
	}
	u, err := d.Users.Create(c.Request.Context(), user.CreateInput{
		Username: req.Username, Email: req.Email, Phone: req.Phone,
		PasswordHash: hash, Nickname: req.Nickname, Role: model.RoleUser,
	})
	if err != nil {
		httpx.ServerError(c, err.Error())
		return
	}
	if req.DeviceID == "" {
		req.DeviceID = "default"
	}
	access, refresh, err := d.JWT.IssuePair(u.ID, req.DeviceID, u.TokenVersion)
	if err != nil {
		httpx.ServerError(c, err.Error())
		return
	}
	_ = d.Users.TouchDevice(c.Request.Context(), u.ID, req.DeviceID, "web", c.GetHeader("User-Agent"))
	httpx.Created(c, gin.H{"user": u, "access_token": access, "refresh_token": refresh})
}

// login 处理 POST /api/v1/auth/login：账号密码登录。
func (d *Deps) login(c *gin.Context) {
	var req struct {
		Login    string `json:"login"`
		Password string `json:"password"`
		DeviceID string `json:"device_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, "invalid_body", err.Error())
		return
	}
	u, err := d.Users.ByLogin(c.Request.Context(), req.Login)
	// 账号不存在与密码错误返回同一错误码，避免向攻击者泄露账号是否存在
	if err != nil || !auth.CheckPassword(u.PasswordHash, req.Password) {
		httpx.Unauthorized(c, "invalid_credentials")
		return
	}
	if u.Status != model.UserStatusNormal {
		httpx.Forbidden(c, "user_disabled")
		return
	}
	if req.DeviceID == "" {
		req.DeviceID = "default"
	}
	access, refresh, err := d.JWT.IssuePair(u.ID, req.DeviceID, u.TokenVersion)
	if err != nil {
		httpx.ServerError(c, err.Error())
		return
	}
	_ = d.Users.TouchDevice(c.Request.Context(), u.ID, req.DeviceID, "web", c.GetHeader("User-Agent"))
	httpx.OK(c, gin.H{"user": u, "access_token": access, "refresh_token": refresh})
}

// refresh 处理 POST /api/v1/auth/refresh：用刷新令牌换发新的令牌对。
func (d *Deps) refresh(c *gin.Context) {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, "invalid_body", err.Error())
		return
	}
	claims, err := d.JWT.Verify(req.RefreshToken)
	if err != nil || claims.Type != auth.TokenTypeRefresh {
		httpx.Unauthorized(c, "invalid_refresh_token")
		return
	}
	u, err := d.Users.ByID(c.Request.Context(), claims.UserID)
	// 通过比对 token_version 实现令牌吊销：登出/封禁会递增版本号，
	// 使该用户此前签发的所有令牌（含 refresh token）立即失效
	if err != nil || u.Status != model.UserStatusNormal || u.TokenVersion != claims.TokenVer {
		httpx.Unauthorized(c, "token_revoked")
		return
	}
	access, refresh, err := d.JWT.IssuePair(u.ID, claims.DeviceID, u.TokenVersion)
	if err != nil {
		httpx.ServerError(c, err.Error())
		return
	}
	httpx.OK(c, gin.H{"access_token": access, "refresh_token": refresh})
}

// logout 处理 POST /api/v1/auth/logout（需登录）：全端登出。
func (d *Deps) logout(c *gin.Context) {
	u := middleware.CurrentUser(c)
	// 登出通过递增 token_version 吊销该用户全部已签发令牌，实现全端强制下线
	if err := d.Users.BumpTokenVersion(c.Request.Context(), u.ID); err != nil {
		httpx.ServerError(c, err.Error())
		return
	}
	d.Audit.Log(audit.Entry{OperatorID: u.ID, Action: "user.logout", TargetType: "user", TargetID: u.ID, IP: c.ClientIP()})
	httpx.OK(c, gin.H{"ok": true})
}
