// Package message 实现消息领域的核心业务逻辑：消息发送（权限校验、敏感词过滤、
// 幂等去重、会话内序号分配、落库、扇出投递）、已读回执、消息撤回，
// 以及历史消息和离线消息的拉取。
// 本包不感知传输层协议；当前仅被 WebSocket 网关（cmd/gateway）使用，
// REST 服务（cmd/api）只做历史消息拉取和已读上报，不经过本包。
package message

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/example/chat/internal/bus"
	"github.com/example/chat/internal/conversation"
	"github.com/example/chat/internal/model"
	"github.com/example/chat/internal/moderation"
	"github.com/example/chat/internal/presence"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// 业务错误集合：由上层 handler 映射为对应的 HTTP 状态码或 WebSocket 错误帧。
var (
	ErrNotMember = errors.New("not a member")
	ErrMuted     = errors.New("you are muted in this conversation")
	ErrEmpty     = errors.New("empty message")
	ErrNotFound  = errors.New("message not found")
)

// Broadcaster 由 ws.Hub 实现，避免 message -> ws 的循环依赖
type Broadcaster interface {
	BroadcastToUsers(ctx context.Context, userIDs []int64, frameType string, data any) error
}

// Service 是消息核心服务，聚合发送链路所需的全部依赖：
// PostgreSQL（消息落库）、Redis（序号分配）、消息总线、会话仓储、在线状态与敏感词引擎。
type Service struct {
	db       *pgxpool.Pool
	rdb      *redis.Client
	bus      bus.Bus
	conv     *conversation.Repo
	presence *presence.Store
	mod      *moderation.Engine
	bc       Broadcaster
}

// NewService 构造消息服务；广播器需之后通过 SetBroadcaster 注入（见 Broadcaster 的说明）。
func NewService(db *pgxpool.Pool, rdb *redis.Client, b bus.Bus,
	conv *conversation.Repo, pr *presence.Store, mod *moderation.Engine) *Service {
	return &Service{db: db, rdb: rdb, bus: b, conv: conv, presence: pr, mod: mod}
}

// SetBroadcaster 延迟注入广播器。ws.Hub 依赖 message.Service，因此只能在
// 二者都构造完成后反向注入，接口参数在注入前调用广播会被安全跳过（s.bc == nil）。
func (s *Service) SetBroadcaster(bc Broadcaster) { s.bc = bc }

// SendInput 是发送消息的入参。ClientMsgID 由客户端生成，是幂等去重的关键：
// 同一会话内重复的 ClientMsgID 会被识别为同一条消息。
type SendInput struct {
	ConversationID int64
	SenderID       int64
	ClientMsgID    string
	Type           int16
	Content        json.RawMessage
	QuoteMsgID     *int64
}

// SendResult 返回发送结果；幂等命中时返回已存在消息的 id/seq/created_at，客户端无感知。
type SendResult struct {
	MessageID int64     `json:"message_id"`
	Seq       int64     `json:"seq"`
	CreatedAt time.Time `json:"created_at"`
}

// Send 处理一条消息的发送全链路：
// 成员/禁言校验 → 敏感词过滤 → 幂等预检 → 分配会话内序号 → 落库 →
// 更新会话最后消息与未读数 → 异步扇出（在线广播 + 离线落表）。
// 幂等性由 (conversation_id, client_msg_id) 唯一约束兜底，客户端重试不会产生重复消息。
// ack 的语义是「已持久化」：返回时消息已落库并完成会话更新，投递由异步扇出完成。
func (s *Service) Send(ctx context.Context, in SendInput) (*SendResult, error) {
	if len(in.Content) == 0 {
		return nil, ErrEmpty
	}
	if in.ClientMsgID == "" {
		return nil, errors.New("client_msg_id required")
	}
	if in.Type == 0 {
		in.Type = model.MsgTypeText
	}

	// 1. 成员 / 禁言校验
	member, err := s.conv.GetMember(ctx, in.ConversationID, in.SenderID)
	if err != nil {
		if errors.Is(err, conversation.ErrNotMember) {
			return nil, ErrNotMember
		}
		return nil, err
	}
	if member.Deleted {
		return nil, ErrNotMember
	}
	if member.MuteUntil != nil && member.MuteUntil.After(time.Now()) {
		return nil, ErrMuted
	}

	// 2. 敏感词过滤
	content, err := s.mod.Filter(in.Type, in.Content)
	if err != nil {
		return nil, err
	}

	// 3. 幂等预检（快速路径）：先查库，命中则直接返回原消息，避免重复分配序号和落库；
	// 并发下的真正兜底靠下面 INSERT 的唯一约束。
	if existing, err := s.findByClientID(ctx, in.ConversationID, in.ClientMsgID); err == nil {
		return &SendResult{MessageID: existing.ID, Seq: existing.Seq, CreatedAt: existing.CreatedAt}, nil
	}

	// 4. 分配会话内序号
	seq, err := s.nextSeq(ctx, in.ConversationID)
	if err != nil {
		return nil, err
	}

	// 5. 落库
	var msg model.Message
	err = s.db.QueryRow(ctx, `
		INSERT INTO messages (conversation_id, seq, sender_id, client_msg_id, type, content, quote_msg_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (conversation_id, client_msg_id) DO NOTHING
		RETURNING id, conversation_id, seq, sender_id, client_msg_id, type, content, quote_msg_id, status, created_at`,
		in.ConversationID, seq, in.SenderID, in.ClientMsgID, in.Type, content, in.QuoteMsgID,
	).Scan(&msg.ID, &msg.ConversationID, &msg.Seq, &msg.SenderID, &msg.ClientMsgID,
		&msg.Type, &msg.Content, &msg.QuoteMsgID, &msg.Status, &msg.CreatedAt)

	if errors.Is(err, pgx.ErrNoRows) {
		// ON CONFLICT DO NOTHING 未插入任何行：说明并发请求已抢先落库，
		// 回查已存在的消息并原样返回，保证重试/并发拿到一致的结果。
		existing, ferr := s.findByClientID(ctx, in.ConversationID, in.ClientMsgID)
		if ferr != nil {
			return nil, ferr
		}
		return &SendResult{MessageID: existing.ID, Seq: existing.Seq, CreatedAt: existing.CreatedAt}, nil
	}
	if err != nil {
		return nil, err
	}

	// 6. 更新会话 & 未读：属于可最终一致的附属状态，失败只记日志不影响发送主流程。
	if err := s.conv.UpdateLastMessage(ctx, in.ConversationID, msg.ID, msg.Seq, msg.CreatedAt); err != nil {
		log.Printf("[message] update conversation failed: %v", err)
	}
	if err := s.conv.BumpUnreadExcept(ctx, in.ConversationID, in.SenderID); err != nil {
		log.Printf("[message] bump unread failed: %v", err)
	}

	// 7. 广播 + 离线落表：异步执行，不阻塞 ack。
	// 借鉴 OpenIM msgtransfer 的思路（落库与投递分离为两个环节）：ack 只反映
	// 持久化耗时，扇出涉及的 Redis/DB 往返不再拖慢发送方确认。
	go s.fanoutAsync(&msg)

	return &SendResult{MessageID: msg.ID, Seq: msg.Seq, CreatedAt: msg.CreatedAt}, nil
}

// fanoutAsync 在独立 goroutine 中执行消息扇出，与发送方 ack 路径解耦：
//   - context 从 context.Background() 派生并带 10s 超时：WS 帧处理的请求 ctx
//     在 Send 返回后即被取消，异步扇出必须与之脱钩，否则投递会被中途掐断；
//   - recover 兜底：扇出 panic 只记日志，绝不影响发送主流程所在 goroutine。
//
// 代价说明：同一会话的并发消息由不同 goroutine 扇出，publish 顺序可能与 seq
// 顺序出现轻微乱序；消息帧自带 seq，客户端按 seq 排序兜底（前端已是这样做的），
// 断线补拉与 sync.pull 也按 seq 增量对齐，因此不影响最终一致性。
func (s *Service) fanoutAsync(msg *model.Message) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[message] fanout panic msg=%d: %v", msg.ID, r)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s.fanout(ctx, msg)
}

// fanout 消息扇出：向会话全体成员广播新消息，并给离线成员写入 offline_messages 表，
// 供其上线后通过 PullOffline 补拉。发送者本人不落离线表。
// 广播与离线写库并行不悖：在线用户实时收到帧，离线/丢帧用户靠离线表兜底。
//
// 性能：在线判断与离线落表都走批量接口——AreOnline 用 Redis Pipeline 一次 RTT
// 判定全部成员，InsertOfflineBatch 单条 SQL 一次落全部离线记录；不再随群人数
// 线性放大往返次数。任一批量步骤失败时回退到逐用户处理，可靠性不低于逐查实现。
func (s *Service) fanout(ctx context.Context, msg *model.Message) {
	memberIDs, err := s.conv.MemberIDs(ctx, msg.ConversationID)
	if err != nil {
		log.Printf("[message] load members failed: %v", err)
		return
	}
	if len(memberIDs) == 0 {
		return
	}

	// 发送者本人不落离线表，先从候选中剔除
	candidates := make([]int64, 0, len(memberIDs))
	for _, uid := range memberIDs {
		if uid != msg.SenderID {
			candidates = append(candidates, uid)
		}
	}

	// 一次 Pipeline 批量判定在线状态；失败时整体降级为逐用户查询
	var offline []int64
	onlineMap, err := s.presence.AreOnline(ctx, candidates)
	if err != nil {
		log.Printf("[message] batch presence check failed, fallback to per-user: %v", err)
		offline = s.collectOfflineSerial(ctx, candidates)
	} else {
		offline = make([]int64, 0, len(candidates))
		for _, uid := range candidates {
			if !onlineMap[uid] {
				offline = append(offline, uid)
			}
		}
	}

	// 单条 SQL 批量落离线表；失败时降级为逐用户插入（同样幂等）
	if len(offline) > 0 {
		if err := s.conv.InsertOfflineBatch(ctx, offline, msg.ConversationID, msg.ID, msg.Seq); err != nil {
			log.Printf("[message] batch insert offline failed, fallback to per-user: %v", err)
			for _, uid := range offline {
				if err := s.conv.InsertOffline(ctx, uid, msg.ConversationID, msg.ID, msg.Seq); err != nil {
					log.Printf("[message] insert offline failed uid=%d: %v", uid, err)
				}
			}
		}
	}

	if s.bc == nil {
		return
	}
	if err := s.bc.BroadcastToUsers(ctx, memberIDs, "message.new", msg); err != nil {
		log.Printf("[message] broadcast failed: %v", err)
	}
}

// collectOfflineSerial 逐个查询在线状态并收集离线成员，是 AreOnline 批量接口
// 失败时的降级路径：单个成员查询失败只跳过该成员（记日志），不影响其他人。
func (s *Service) collectOfflineSerial(ctx context.Context, candidates []int64) []int64 {
	offline := make([]int64, 0, len(candidates))
	for _, uid := range candidates {
		online, err := s.presence.IsOnline(ctx, uid)
		if err != nil {
			log.Printf("[message] presence check failed uid=%d: %v", uid, err)
			continue
		}
		if !online {
			offline = append(offline, uid)
		}
	}
	return offline
}

// nextSeq 分配会话内单调递增的消息序号。优先用 Redis INCR（高性能），
// Redis 不可用时降级为直接更新 conversations.max_seq（DB 序列兜底）；
// Redis key 首次创建（返回 1）时用 DB 的 max_seq 校准，避免重启后序号回退造成乱序。
func (s *Service) nextSeq(ctx context.Context, convID int64) (int64, error) {
	key := fmt.Sprintf("conv:%d:seq", convID)
	seq, err := s.rdb.Incr(ctx, key).Result()
	if err != nil {
		// Redis 不可用：降级到 DB 序列
		var dbSeq int64
		if err := s.db.QueryRow(ctx, `
			UPDATE conversations SET max_seq = max_seq + 1 WHERE id=$1 RETURNING max_seq`, convID).Scan(&dbSeq); err != nil {
			return 0, err
		}
		return dbSeq, nil
	}
	// 首次分配时用 DB 的 max_seq 校准
	if seq == 1 {
		var dbMax int64
		_ = s.db.QueryRow(ctx, `SELECT max_seq FROM conversations WHERE id=$1`, convID).Scan(&dbMax)
		if dbMax > 0 {
			seq, err = s.rdb.IncrBy(ctx, key, dbMax).Result()
			if err != nil {
				return 0, err
			}
		}
	}
	return seq, nil
}

// findByClientID 按 (会话, 客户端消息ID) 查找已存在的消息，供幂等去重使用。
func (s *Service) findByClientID(ctx context.Context, convID int64, clientMsgID string) (*model.Message, error) {
	var m model.Message
	err := s.db.QueryRow(ctx, `
		SELECT id, conversation_id, seq, sender_id, client_msg_id, type, content, quote_msg_id, status, created_at
		FROM messages WHERE conversation_id=$1 AND client_msg_id=$2`, convID, clientMsgID).
		Scan(&m.ID, &m.ConversationID, &m.Seq, &m.SenderID, &m.ClientMsgID,
			&m.Type, &m.Content, &m.QuoteMsgID, &m.Status, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &m, err
}

// ReadInput 是标记已读的入参；LastReadSeq 为客户端已读到的最大会话内序号。
type ReadInput struct {
	ConversationID int64
	UserID         int64
	LastReadSeq    int64
}

// MarkRead 更新成员已读位置并清零未读数，然后向会话内广播 "message.read" 事件，
// 让对端实时刷新已读回执（如"对方已读"标记）。
func (s *Service) MarkRead(ctx context.Context, in ReadInput) error {
	if _, err := s.conv.GetMember(ctx, in.ConversationID, in.UserID); err != nil {
		return ErrNotMember
	}
	if err := s.conv.MarkRead(ctx, in.ConversationID, in.UserID, in.LastReadSeq); err != nil {
		return err
	}
	if s.bc != nil {
		memberIDs, err := s.conv.MemberIDs(ctx, in.ConversationID)
		if err == nil {
			_ = s.bc.BroadcastToUsers(ctx, memberIDs, "message.read", map[string]any{
				"conversation_id": in.ConversationID,
				"user_id":         in.UserID,
				"last_read_seq":   in.LastReadSeq,
			})
		}
	}
	return nil
}

// RecallInput 是撤回消息的入参；OperatorID 即撤回操作者（必须是消息发送者本人，由仓储层校验）。
type RecallInput struct {
	ConversationID int64
	MessageID      int64
	OperatorID     int64
}

// Recall 撤回消息：先落库改状态，再广播 "message.recall" 事件让各端即时移除该消息的展示。
func (s *Service) Recall(ctx context.Context, in RecallInput) error {
	if _, err := s.conv.GetMember(ctx, in.ConversationID, in.OperatorID); err != nil {
		return ErrNotMember
	}
	if err := s.conv.RecallMessage(ctx, in.ConversationID, in.MessageID, in.OperatorID); err != nil {
		return err
	}
	if s.bc != nil {
		memberIDs, err := s.conv.MemberIDs(ctx, in.ConversationID)
		if err == nil {
			_ = s.bc.BroadcastToUsers(ctx, memberIDs, "message.recall", map[string]any{
				"conversation_id": in.ConversationID,
				"message_id":      in.MessageID,
				"operator_id":     in.OperatorID,
			})
		}
	}
	return nil
}

// Pull 按 seq 游标增量拉取会话历史消息（仅成员可用），用于翻页和断线后补齐缺口。
func (s *Service) Pull(ctx context.Context, convID, userID, fromSeq int64, limit int) ([]*model.Message, error) {
	if _, err := s.conv.GetMember(ctx, convID, userID); err != nil {
		return nil, ErrNotMember
	}
	return s.conv.ListMessages(ctx, convID, fromSeq, limit)
}

// PullOffline 拉取用户离线期间积压的消息（来自 offline_messages 表），通常在 WebSocket 重连后调用。
func (s *Service) PullOffline(ctx context.Context, userID int64, limit int) ([]*model.Message, error) {
	return s.conv.PullOffline(ctx, userID, limit)
}
