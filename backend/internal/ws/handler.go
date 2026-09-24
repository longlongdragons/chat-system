package ws

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/example/chat/internal/auth"
	"github.com/example/chat/internal/conversation"
	"github.com/example/chat/internal/message"
	"github.com/example/chat/internal/model"
	"github.com/example/chat/internal/presence"
	"github.com/example/chat/internal/user"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// Handler 是 WebSocket 连接的入口处理器：负责 /ws 路由的握手鉴权与升级、
// 连接建立后的初始化（注册、上线广播、离线消息补拉），以及把客户端上行帧
// 分发到对应的业务处理逻辑（发消息、已读、撤回、补拉、输入中）。
type Handler struct {
	jwt      *auth.Manager
	users    *user.Repo
	conv     *conversation.Repo
	messages *message.Service
	presence *presence.Store
	hub      *Hub

	allowedOrigins []string
	upgrader       websocket.Upgrader
}

// NewHandler 组装连接处理器；allowedOrigins 用于 WebSocket 握手的
// Origin 校验，防止浏览器端跨站劫持（CSWSH）。
func NewHandler(jwtMgr *auth.Manager, users *user.Repo, conv *conversation.Repo,
	messages *message.Service, pr *presence.Store, hub *Hub, allowedOrigins []string) *Handler {

	h := &Handler{
		jwt: jwtMgr, users: users, conv: conv, messages: messages,
		presence: pr, hub: hub, allowedOrigins: allowedOrigins,
	}
	h.upgrader = websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			if origin == "" {
				return true // 非浏览器客户端（如移动端）
			}
			for _, o := range allowedOrigins {
				if strings.EqualFold(strings.TrimSpace(o), origin) {
					return true
				}
			}
			return false
		},
	}
	return h
}

// ServeWS gin 路由：GET /ws?token=...&device_id=...
// 流程：鉴权 -> 升级 WebSocket -> 注册连接 -> 下发 connected 帧 ->
// 按需广播上线 -> 异步补拉离线消息 -> 进入读循环（阻塞至连接结束）。
func (h *Handler) ServeWS(c *gin.Context) {
	token := c.Query("token")
	deviceID := c.Query("device_id")
	if deviceID == "" {
		deviceID = "unknown"
	}
	if token == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"code": "missing_token"})
		return
	}

	// WebSocket 握手无法携带 Authorization 头，token 走查询参数；
	// 这里只接受 access token，refresh token 不允许建立长连接。
	claims, err := h.jwt.Verify(token)
	if err != nil || claims.Type != auth.TokenTypeAccess {
		c.JSON(http.StatusUnauthorized, gin.H{"code": "invalid_token"})
		return
	}
	// 二次校验用户状态与 TokenVersion：账号被封禁或「踢下线/改密」导致
	// TokenVersion 递增后，旧 token 签发的连接一律拒绝。
	u, err := h.users.ByID(c.Request.Context(), claims.UserID)
	if err != nil || u.Status != model.UserStatusNormal || u.TokenVersion != claims.TokenVer {
		c.JSON(http.StatusUnauthorized, gin.H{"code": "token_revoked"})
		return
	}

	wsConn, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("[ws] upgrade failed: %v", err)
		return
	}

	conn := newConn(wsConn, h.hub.newConnID(), u.ID, deviceID, h.hub.ServerID(), h.hub)
	firstOnline, err := h.hub.Register(conn)
	if err != nil {
		conn.Close("register_failed")
		return
	}
	// 记录设备最近活跃信息（平台/UA），失败不影响连接。
	_ = h.users.TouchDevice(c.Request.Context(), u.ID, deviceID, c.GetHeader("X-Platform"), c.GetHeader("User-Agent"))

	go conn.writePump()

	conn.SendFrame("connected", map[string]any{
		"user_id":    u.ID,
		"session_id": conn.ID,
		"server_ts":  time.Now().Unix(),
	})

	// 只有「该用户全平台第一条连接」才广播上线，避免多端重复提醒。
	if firstOnline {
		h.hub.BroadcastPresence(context.Background(), u.ID, true)
	}

	// 上线补拉离线消息（异步）
	go h.deliverOffline(conn)

	conn.readPump(h)
}

// deliverOffline 连接建立后异步补拉离线期间积压的消息，一次性批量下发；
// 失败只记日志，客户端后续仍可通过 sync.pull 增量补拉兜底。
func (h *Handler) deliverOffline(c *Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	msgs, err := h.messages.PullOffline(ctx, c.UserID, 500)
	if err != nil {
		log.Printf("[ws] pull offline failed user=%d: %v", c.UserID, err)
		return
	}
	if len(msgs) == 0 {
		return
	}
	c.SendFrame("offline.batch", map[string]any{"messages": msgs})
}

// Dispatch 帧分发：按帧类型路由到对应处理器。每个帧单独派生 8 秒超时的
// 上下文，防止单个慢请求（如数据库抖动）长时间占用连接读循环。
func (h *Handler) Dispatch(c *Conn, f *Frame) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	switch f.T {
	case "ping":
		// 应用层心跳：回 pong 并刷新 Redis 在线状态 TTL，
		// 避免长连接空闲期间在线状态过期。
		c.SendFrame("pong", map[string]any{})
		h.presence.Heartbeat(ctx, c.UserID)

	case "message.send":
		h.onMessageSend(ctx, c, f)

	case "message.read":
		h.onMessageRead(ctx, c, f)

	case "message.recall":
		h.onMessageRecall(ctx, c, f)

	case "sync.pull":
		h.onSyncPull(ctx, c, f)

	case "typing":
		h.onTyping(ctx, c, f)

	default:
		c.SendError("unknown_type", "unsupported frame type: "+f.T, f.ID)
	}
}

// onMessageSend 处理 message.send：调用消息服务完成敏感词过滤、幂等去重、
// 落库与序号分配、扇出广播，然后回 message.ack 告知客户端发送结果。
func (h *Handler) onMessageSend(ctx context.Context, c *Conn, f *Frame) {
	var p sendPayload
	if err := json.Unmarshal(f.Data, &p); err != nil {
		c.SendError("bad_payload", "invalid message.send payload", f.ID)
		return
	}
	// 客户端没带幂等键时由服务端补一个，保证下游逻辑总能按 client_msg_id 去重。
	if p.ClientMsgID == "" {
		p.ClientMsgID = uuid.NewString()
	}

	res, err := h.messages.Send(ctx, message.SendInput{
		ConversationID: p.ConversationID,
		SenderID:       c.UserID,
		ClientMsgID:    p.ClientMsgID,
		Type:           p.Type,
		Content:        p.Content,
		QuoteMsgID:     p.QuoteMsgID,
	})
	if err != nil {
		code, msg := mapSendError(err)
		c.SendError(code, msg, f.ID)
		return
	}
	c.SendFrame("message.ack", map[string]any{
		"client_msg_id":   p.ClientMsgID,
		"message_id":      res.MessageID,
		"conversation_id": p.ConversationID,
		"seq":             res.Seq,
		"created_at":      res.CreatedAt,
	})
}

// onMessageRead 处理 message.read：已读进度上报，服务端推进该用户在该会话
// 的已读序号并向其他成员广播已读回执。失败只回错误帧，不重试（客户端可再次上报）。
func (h *Handler) onMessageRead(ctx context.Context, c *Conn, f *Frame) {
	var p readPayload
	if err := json.Unmarshal(f.Data, &p); err != nil {
		c.SendError("bad_payload", "invalid message.read payload", f.ID)
		return
	}
	if err := h.messages.MarkRead(ctx, message.ReadInput{
		ConversationID: p.ConversationID,
		UserID:         c.UserID,
		LastReadSeq:    p.LastReadSeq,
	}); err != nil {
		c.SendError("read_failed", err.Error(), f.ID)
	}
}

// onMessageRecall 处理 message.recall：撤回消息，权限与时限校验在消息服务内完成。
func (h *Handler) onMessageRecall(ctx context.Context, c *Conn, f *Frame) {
	var p recallPayload
	if err := json.Unmarshal(f.Data, &p); err != nil {
		c.SendError("bad_payload", "invalid message.recall payload", f.ID)
		return
	}
	if err := h.messages.Recall(ctx, message.RecallInput{
		ConversationID: p.ConversationID,
		MessageID:      p.MessageID,
		OperatorID:     c.UserID,
	}); err != nil {
		c.SendError("recall_failed", err.Error(), f.ID)
	}
}

// onSyncPull 处理 sync.pull：断线重连或翻历史时按序号增量拉取消息，
// 以 sync.batch 帧返回。
func (h *Handler) onSyncPull(ctx context.Context, c *Conn, f *Frame) {
	var p syncPullPayload
	if err := json.Unmarshal(f.Data, &p); err != nil {
		c.SendError("bad_payload", "invalid sync.pull payload", f.ID)
		return
	}
	// 限制单批大小，防止一次拉取过大拖垮连接与数据库。
	if p.Limit <= 0 || p.Limit > 200 {
		p.Limit = 100
	}
	msgs, err := h.messages.Pull(ctx, p.ConversationID, c.UserID, p.FromSeq, p.Limit)
	if err != nil {
		c.SendError("pull_failed", err.Error(), f.ID)
		return
	}
	c.SendFrame("sync.batch", map[string]any{
		"conversation_id": p.ConversationID,
		"messages":        msgs,
		"from_seq":        p.FromSeq,
	})
}

// onTyping 处理 typing：输入中提示是纯实时信号，不落库、不回 ack，
// 任何一步失败都静默丢弃（这类帧丢失无业务影响）。
func (h *Handler) onTyping(ctx context.Context, c *Conn, f *Frame) {
	var p typingPayload
	if err := json.Unmarshal(f.Data, &p); err != nil {
		return
	}
	memberIDs, err := h.conv.MemberIDs(ctx, p.ConversationID)
	if err != nil {
		return
	}
	// 扇出目标为会话内除自己以外的全部成员（多端在线时由 hub 逐连接投递）。
	targets := make([]int64, 0, len(memberIDs))
	for _, id := range memberIDs {
		if id != c.UserID {
			targets = append(targets, id)
		}
	}
	_ = h.hub.BroadcastToUsers(ctx, targets, "typing", map[string]any{
		"conversation_id": p.ConversationID,
		"user_id":         c.UserID,
		"ts":              time.Now().Unix(),
	})
}

// mapSendError 把消息服务的领域错误映射为客户端可理解的错误码，
// 前端据此区分「非成员 / 被禁言 / 空消息 / 命中敏感词」等提示文案。
func mapSendError(err error) (code, msg string) {
	switch {
	case err == message.ErrNotMember:
		return "not_member", "you are not a member of this conversation"
	case err == message.ErrMuted:
		return "muted", "you are muted in this conversation"
	case err == message.ErrEmpty:
		return "empty_message", "message content is empty"
	default:
		if strings.Contains(err.Error(), "blocked by moderation") {
			return "sensitive_blocked", "message blocked by content policy"
		}
		return "send_failed", err.Error()
	}
}
