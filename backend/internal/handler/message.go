package handler

import (
	"strconv"

	"github.com/example/chat/internal/httpx"
	"github.com/example/chat/internal/middleware"
	"github.com/gin-gonic/gin"
)

// listMessages 处理 GET /api/v1/conversations/:id/messages：按序号增量拉取历史消息。
func (d *Deps) listMessages(c *gin.Context) {
	convID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	fromSeq, _ := strconv.ParseInt(c.DefaultQuery("from_seq", "0"), 10, 64)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	u := middleware.CurrentUser(c)

	// 只有会话成员才能拉取历史消息，防止越权读取他人聊天内容
	if _, err := d.Convs.GetMember(c.Request.Context(), convID, u.ID); err != nil {
		httpx.Forbidden(c, "not_member")
		return
	}
	// 按消息序号增量拉取：返回 seq > from_seq 的 limit 条，供客户端翻页/补拉离线消息
	msgs, err := d.Convs.ListMessages(c.Request.Context(), convID, fromSeq, limit)
	if err != nil {
		httpx.ServerError(c, err.Error())
		return
	}
	httpx.OK(c, gin.H{"messages": msgs})
}

// markRead 处理 POST /api/v1/conversations/:id/read：上报已读位置。
func (d *Deps) markRead(c *gin.Context) {
	convID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct {
		LastReadSeq int64 `json:"last_read_seq" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, "invalid_body", err.Error())
		return
	}
	u := middleware.CurrentUser(c)
	// 上报已读位置（last_read_seq），服务端据此计算未读数并生成已读回执；
	// 该操作幂等，重复上报同一序号无副作用
	if err := d.Convs.MarkRead(c.Request.Context(), convID, u.ID, req.LastReadSeq); err != nil {
		httpx.ServerError(c, err.Error())
		return
	}
	httpx.OK(c, gin.H{"ok": true})
}
