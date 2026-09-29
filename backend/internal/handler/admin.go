package handler

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/example/chat/internal/audit"
	"github.com/example/chat/internal/httpx"
	"github.com/example/chat/internal/middleware"
	"github.com/example/chat/internal/model"
	"github.com/gin-gonic/gin"
)

// createReport 处理 POST /api/v1/reports（需登录）：用户举报入口。
// target_type 区分被举报对象（用户/消息等），仅入库留痕，后续由管理后台人工处理。
func (d *Deps) createReport(c *gin.Context) {
	var req struct {
		TargetType int16  `json:"target_type" binding:"required"`
		TargetID   int64  `json:"target_id" binding:"required"`
		Reason     string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, "invalid_body", err.Error())
		return
	}
	u := middleware.CurrentUser(c)
	_, err := d.DB.Exec(c.Request.Context(), `
		INSERT INTO reports (reporter_id, target_type, target_id, reason)
		VALUES ($1,$2,$3,$4)`, u.ID, req.TargetType, req.TargetID, req.Reason)
	if err != nil {
		httpx.ServerError(c, err.Error())
		return
	}
	httpx.Created(c, gin.H{"ok": true})
}

// adminListUsers 处理 GET /api/v1/admin/users：管理后台用户分页查询。
func (d *Deps) adminListUsers(c *gin.Context) {
	keyword := c.Query("keyword")
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	list, total, err := d.Users.ListAdmin(c.Request.Context(), keyword, offset, limit)
	if err != nil {
		httpx.ServerError(c, err.Error())
		return
	}
	httpx.OK(c, gin.H{"users": list, "total": total})
}

// adminBanUser 处理 POST /api/v1/admin/users/:id/ban：封禁用户并强制其全端下线。
func (d *Deps) adminBanUser(c *gin.Context) {
	targetID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct {
		Reason   string `json:"reason"`
		ExpireAt string `json:"expire_at"`
	}
	_ = c.ShouldBindJSON(&req)

	operator := middleware.CurrentUser(c)
	if err := d.Users.SetStatus(c.Request.Context(), targetID, model.UserStatusBanned); err != nil {
		httpx.ServerError(c, err.Error())
		return
	}
	// 封禁后立即递增 token_version，强制被ban用户所有在线会话下线
	_ = d.Users.BumpTokenVersion(c.Request.Context(), targetID)

	// expire_at 为空表示永久封禁；格式非法时静默忽略，不影响封禁主流程
	var expireAt *time.Time
	if req.ExpireAt != "" {
		if t, err := time.Parse(time.RFC3339, req.ExpireAt); err == nil {
			expireAt = &t
		}
	}
	_, _ = d.DB.Exec(c.Request.Context(), `
		INSERT INTO bans (user_id, type, reason, expire_at, operator)
		VALUES ($1, 1, $2, $3, $4)`, targetID, req.Reason, expireAt, operator.ID)

	d.Audit.Log(audit.Entry{
		OperatorID: operator.ID, Action: "user.ban",
		TargetType: "user", TargetID: targetID,
		Detail: gin.H{"reason": req.Reason}, IP: c.ClientIP(),
	})
	httpx.OK(c, gin.H{"ok": true})
}

// adminListAudit 处理 GET /api/v1/admin/audit：分页查看操作审计日志（按 id 倒序）。
func (d *Deps) adminListAudit(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := d.DB.Query(c.Request.Context(), `
		SELECT id, operator_id, action, target_type, target_id, detail, ip, created_at
		FROM audit_logs ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		httpx.ServerError(c, err.Error())
		return
	}
	defer rows.Close()
	type row struct {
		ID         int64           `json:"id"`
		OperatorID int64           `json:"operator_id"`
		Action     string          `json:"action"`
		TargetType *string         `json:"target_type"`
		TargetID   *int64          `json:"target_id"`
		Detail     json.RawMessage `json:"detail"`
		IP         *string         `json:"ip"`
		CreatedAt  time.Time       `json:"created_at"`
	}
	var out []row
	for rows.Next() {
		var rr row
		if err := rows.Scan(&rr.ID, &rr.OperatorID, &rr.Action, &rr.TargetType,
			&rr.TargetID, &rr.Detail, &rr.IP, &rr.CreatedAt); err != nil {
			httpx.ServerError(c, err.Error())
			return
		}
		out = append(out, rr)
	}
	httpx.OK(c, gin.H{"logs": out})
}
