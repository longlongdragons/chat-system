package handler

import (
	"strconv"

	"github.com/example/chat/internal/httpx"
	"github.com/example/chat/internal/middleware"
	"github.com/example/chat/internal/model"
	"github.com/gin-gonic/gin"
)

// listConversations 处理 GET /api/v1/conversations：当前用户的会话列表。
func (d *Deps) listConversations(c *gin.Context) {
	u := middleware.CurrentUser(c)
	views, err := d.Convs.ListForUser(c.Request.Context(), u.ID)
	if err != nil {
		httpx.ServerError(c, err.Error())
		return
	}
	// 附加单聊对端资料：会话列表只含会话本身信息，这里批量查出
	// 每个单聊的对端用户并回填，便于前端直接渲染会话标题/头像
	convIDs := make([]int64, 0, len(views))
	for _, v := range views {
		convIDs = append(convIDs, v.ID)
	}
	peers, _ := d.Convs.PeerIDs(c.Request.Context(), convIDs, u.ID)
	peerIDs := make([]int64, 0, len(peers))
	for _, pid := range peers {
		peerIDs = append(peerIDs, pid)
	}
	peerMap, _ := d.Users.ListByIDs(c.Request.Context(), peerIDs)
	for _, v := range views {
		if pid, ok := peers[v.ID]; ok {
			v.Peer = peerMap[pid]
		}
	}
	httpx.OK(c, gin.H{"conversations": views})
}

// createDirect 处理 POST /api/v1/conversations/direct：发起单聊。
func (d *Deps) createDirect(c *gin.Context) {
	var req struct {
		UserID int64 `json:"user_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, "invalid_body", err.Error())
		return
	}
	u := middleware.CurrentUser(c)
	// 创建（或复用）与目标用户的单聊会话；同一对用户重复调用返回同一会话，保证幂等
	convID, err := d.Convs.CreateDirect(c.Request.Context(), u.ID, req.UserID)
	if err != nil {
		httpx.BadRequest(c, "create_direct_failed", err.Error())
		return
	}
	conv, err := d.Convs.Get(c.Request.Context(), convID)
	if err != nil {
		httpx.ServerError(c, err.Error())
		return
	}
	httpx.OK(c, conv)
}

// createGroup 处理 POST /api/v1/conversations/group：创建群聊。
func (d *Deps) createGroup(c *gin.Context) {
	var req struct {
		Name      string  `json:"name" binding:"required"`
		MemberIDs []int64 `json:"member_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, "invalid_body", err.Error())
		return
	}
	u := middleware.CurrentUser(c)
	convID, err := d.Convs.CreateGroup(c.Request.Context(), u.ID, req.Name, req.MemberIDs)
	if err != nil {
		httpx.BadRequest(c, "create_group_failed", err.Error())
		return
	}
	conv, err := d.Convs.Get(c.Request.Context(), convID)
	if err != nil {
		httpx.ServerError(c, err.Error())
		return
	}
	httpx.Created(c, conv)
}

// addMembers 处理 POST /api/v1/conversations/:id/members：拉人入群。
func (d *Deps) addMembers(c *gin.Context) {
	convID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct {
		UserIDs []int64 `json:"user_ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, "invalid_body", err.Error())
		return
	}
	u := middleware.CurrentUser(c)
	// 拉人入群仅群主/管理员有权限
	member, err := d.Convs.GetMember(c.Request.Context(), convID, u.ID)
	if err != nil || (member.Role != model.MemberRoleOwner && member.Role != model.MemberRoleAdmin) {
		httpx.Forbidden(c, "no_permission")
		return
	}
	if err := d.Convs.AddMembers(c.Request.Context(), convID, req.UserIDs); err != nil {
		httpx.BadRequest(c, "add_members_failed", err.Error())
		return
	}
	httpx.OK(c, gin.H{"ok": true})
}

// removeMember 处理 DELETE /api/v1/conversations/:id/members/:uid：踢人出群。
func (d *Deps) removeMember(c *gin.Context) {
	convID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	targetID, _ := strconv.ParseInt(c.Param("uid"), 10, 64)
	u := middleware.CurrentUser(c)
	// 踢人出群同样仅群主/管理员有权限
	member, err := d.Convs.GetMember(c.Request.Context(), convID, u.ID)
	if err != nil || (member.Role != model.MemberRoleOwner && member.Role != model.MemberRoleAdmin) {
		httpx.Forbidden(c, "no_permission")
		return
	}
	if err := d.Convs.RemoveMember(c.Request.Context(), convID, targetID); err != nil {
		httpx.ServerError(c, err.Error())
		return
	}
	httpx.OK(c, gin.H{"ok": true})
}
