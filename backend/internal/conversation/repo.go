// Package conversation 提供会话（conversation）领域的持久化仓储：
// 单聊创建与去重（direct_pairs 表保证同一对用户只有一个会话）、群聊创建与成员管理、
// 已读回执与未读数维护、离线消息存取、历史消息游标拉取以及消息撤回。
// 所有读写均直接操作 PostgreSQL，不含传输层和实时投递逻辑。
package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/example/chat/internal/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// 仓储层业务错误，由上层（service/handler）映射为对外错误响应。
var (
	ErrNotFound    = errors.New("conversation not found")
	ErrNotMember   = errors.New("not a member")
	ErrSelfChat    = errors.New("cannot chat with yourself")
	ErrMemberLimit = errors.New("member limit exceeded")
)

// Repo 会话仓储，封装 conversations / conversation_members / direct_pairs /
// groups / message_receipts / offline_messages 等表的 SQL 访问。
type Repo struct {
	db *pgxpool.Pool
}

// NewRepo 构造会话仓储。
func NewRepo(db *pgxpool.Pool) *Repo { return &Repo{db: db} }

// CreateDirect 创建或复用单聊。
// 通过把 (a, b) 归一化为 user_a < user_b 并借助 direct_pairs 的唯一约束，
// 保证同一对用户无论谁先发起都复用同一个会话，不会产生重复单聊。
func (r *Repo) CreateDirect(ctx context.Context, a, b int64) (int64, error) {
	if a == b {
		return 0, ErrSelfChat
	}
	if a > b {
		a, b = b, a
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var convID int64
	// 先查已有配对：命中则直接复用旧会话（同事务内提交以释放锁）。
	err = tx.QueryRow(ctx,
		`SELECT conversation_id FROM direct_pairs WHERE user_a=$1 AND user_b=$2`, a, b).Scan(&convID)
	if err == nil {
		return convID, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}

	if err := tx.QueryRow(ctx,
		`INSERT INTO conversations (type) VALUES ($1) RETURNING id`, model.ConvTypeDirect).Scan(&convID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO direct_pairs (conversation_id, user_a, user_b) VALUES ($1,$2,$3)`,
		convID, a, b); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO conversation_members (conversation_id, user_id) VALUES ($1,$2), ($1,$3)`,
		convID, a, b); err != nil {
		return 0, err
	}
	return convID, tx.Commit(ctx)
}

// CreateGroup 创建群聊：同事务写入会话主体、groups 扩展表和成员表，
// 群主以 owner 角色入成员表；初始成员列表先去重并剔除群主，再批量插入。
func (r *Repo) CreateGroup(ctx context.Context, owner int64, name string, memberIDs []int64) (int64, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var convID int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO conversations (type, title, owner_id) VALUES ($1,$2,$3) RETURNING id`,
		model.ConvTypeGroup, name, owner).Scan(&convID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO groups (conversation_id, name) VALUES ($1,$2)`, convID, name); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO conversation_members (conversation_id, user_id, role) VALUES ($1,$2,$3)`,
		convID, owner, model.MemberRoleOwner); err != nil {
		return 0, err
	}

	seen := map[int64]bool{owner: true}
	ids := make([]int64, 0, len(memberIDs))
	for _, id := range memberIDs {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) > 0 {
		// unnest 批量插入 + ON CONFLICT DO NOTHING：防御并发下重复加人导致的唯一键冲突。
		if _, err := tx.Exec(ctx, `
			INSERT INTO conversation_members (conversation_id, user_id, role)
			SELECT $1, uid, $2 FROM unnest($3::bigint[]) AS uid
			ON CONFLICT DO NOTHING`, convID, model.MemberRoleMember, ids); err != nil {
			return 0, err
		}
	}
	return convID, tx.Commit(ctx)
}

// Get 按 ID 查询会话基本信息；status=1 过滤掉已解散/删除的会话。
func (r *Repo) Get(ctx context.Context, id int64) (*model.Conversation, error) {
	var c model.Conversation
	err := r.db.QueryRow(ctx, `
		SELECT id, type, title, owner_id, last_msg_id, last_msg_at, max_seq, status, created_at
		FROM conversations WHERE id=$1 AND status=1`, id).
		Scan(&c.ID, &c.Type, &c.Title, &c.OwnerID, &c.LastMsgID, &c.LastMsgAt,
			&c.MaxSeq, &c.Status, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &c, err
}

// GetMember 查询用户在会话中的成员记录（角色、已读位置、未读数、免打扰/禁言等状态），
// 是发送、撤回、已读等操作前的权限校验入口。
func (r *Repo) GetMember(ctx context.Context, convID, userID int64) (*model.Member, error) {
	var m model.Member
	err := r.db.QueryRow(ctx, `
		SELECT conversation_id, user_id, role, last_read_seq, unread_count,
		       pinned, muted, deleted, mute_until, joined_at
		FROM conversation_members WHERE conversation_id=$1 AND user_id=$2`, convID, userID).
		Scan(&m.ConversationID, &m.UserID, &m.Role, &m.LastReadSeq, &m.UnreadCount,
			&m.Pinned, &m.Muted, &m.Deleted, &m.MuteUntil, &m.JoinedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotMember
	}
	return &m, err
}

// MemberIDs 返回会话全部有效成员（不含已软删除者）的用户 ID，供消息扇出和事件广播使用。
func (r *Repo) MemberIDs(ctx context.Context, convID int64) ([]int64, error) {
	rows, err := r.db.Query(ctx,
		`SELECT user_id FROM conversation_members WHERE conversation_id=$1 AND deleted=false`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ListForUser 查询用户的会话列表（含最后一条消息、未读、单聊对端）。
// 一条 SQL 联查成员状态、会话主体、最后一条消息和单聊配对表；
// 排序规则：置顶优先，其余按最后消息时间倒序， capped 300 条防止大账户全表扫描。
func (r *Repo) ListForUser(ctx context.Context, userID int64) ([]*model.ConversationView, error) {
	rows, err := r.db.Query(ctx, `
		SELECT c.id, c.type, c.title, c.owner_id, c.last_msg_id, c.last_msg_at,
		       c.max_seq, c.status, c.created_at,
		       cm.role, cm.last_read_seq, cm.unread_count, cm.pinned, cm.muted, cm.joined_at,
		       m.id, m.conversation_id, m.seq, m.sender_id, m.client_msg_id,
		       m.type, m.content, m.quote_msg_id, m.status, m.created_at,
		       dp.user_a, dp.user_b
		FROM conversation_members cm
		JOIN conversations c ON c.id = cm.conversation_id
		LEFT JOIN messages m ON m.id = c.last_msg_id
		LEFT JOIN direct_pairs dp ON dp.conversation_id = c.id
		WHERE cm.user_id = $1 AND cm.deleted = false AND c.status = 1
		ORDER BY cm.pinned DESC, c.last_msg_at DESC NULLS LAST, c.id DESC
		LIMIT 300`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var views []*model.ConversationView
	peerIDs := make([]int64, 0, 8)
	peerOf := make(map[int64]int64) // convID -> peerID

	for rows.Next() {
		v := &model.ConversationView{}
		var (
			lastID, lastSeq, lastSenderID *int64
			lastClientID                  *string
			lastType                      *int16
			lastContent                   []byte
			lastQuote                     *int64
			lastStatus                    *int16
			lastCreated                   *time.Time
			userA, userB                  *int64
		)
		if err := rows.Scan(
			&v.ID, &v.Type, &v.Title, &v.OwnerID, &v.LastMsgID, &v.LastMsgAt,
			&v.MaxSeq, &v.Status, &v.CreatedAt,
			&v.Member.Role, &v.Member.LastReadSeq, &v.Member.UnreadCount,
			&v.Member.Pinned, &v.Member.Muted, &v.Member.JoinedAt,
			&lastID, &lastSeq, &lastSenderID, &lastClientID, &lastType,
			&lastContent, &lastQuote, &lastStatus, &lastCreated,
			&userA, &userB,
		); err != nil {
			return nil, err
		}
		v.Member.ConversationID = v.ID
		v.Member.UserID = userID

		if lastID != nil {
			// 会话还没有任何消息时 LEFT JOIN 出全 NULL 行，这里仅在确有最后消息时才组装。
			v.LastMsg = &model.Message{
				ID: *lastID, ConversationID: v.ID,
				Seq: derefInt64(lastSeq), SenderID: derefInt64(lastSenderID),
				ClientMsgID: derefStr(lastClientID), Type: derefInt16(lastType),
				Content: json.RawMessage(lastContent), QuoteMsgID: lastQuote,
				Status: derefInt16(lastStatus), CreatedAt: derefTime(lastCreated),
			}
		}

		if v.Type == model.ConvTypeDirect && userA != nil && userB != nil {
			// 单聊会话从配对表中算出"对端"用户 ID，供上层补充对端昵称/头像展示。
			peer := *userA
			if peer == userID {
				peer = *userB
			}
			peerOf[v.ID] = peer
			peerIDs = append(peerIDs, peer)
		}
		views = append(views, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return views, nil
}

// AttachPeers 预留接口：原计划为会话列表批量挂载单聊对端用户信息，当前尚未实现
// （查询结果被丢弃，直接返回 nil）。对端信息目前由 PeerIDs + 上层 load 回调完成。
func (r *Repo) AttachPeers(ctx context.Context, views []*model.ConversationView,
	load func(ctx context.Context, ids []int64) (map[int64]*model.User, error)) error {
	ids := make([]int64, 0)
	for _, v := range views {
		if v.Type == model.ConvTypeDirect {
			ids = append(ids, v.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	// 重查对端
	rows, err := r.db.Query(ctx, `
		SELECT conversation_id, CASE WHEN user_a = $1 THEN user_b ELSE user_a END
		FROM direct_pairs WHERE conversation_id = ANY($2)`, 0, ids)
	_ = rows
	_ = err
	return nil
}

// PeerIDs 批量求解一批单聊会话中"我"的对端用户 ID，返回 convID -> peerID 映射，
// 用 CASE WHEN 在一次查询中完成对端取反，避免逐会话查询。
func (r *Repo) PeerIDs(ctx context.Context, convIDs []int64, self int64) (map[int64]int64, error) {
	out := map[int64]int64{}
	if len(convIDs) == 0 {
		return out, nil
	}
	rows, err := r.db.Query(ctx, `
		SELECT conversation_id, CASE WHEN user_a = $1 THEN user_b ELSE user_a END
		FROM direct_pairs WHERE conversation_id = ANY($2)`, self, convIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, pid int64
		if err := rows.Scan(&cid, &pid); err != nil {
			return nil, err
		}
		out[cid] = pid
	}
	return out, rows.Err()
}

// AddMembers 向群聊批量加人。先按 groups.member_limit 做人数上限校验；
// 插入用 ON CONFLICT DO UPDATE 把曾被软删除（退群/被踢）的成员"复活"，
// 而不是报唯一键冲突或产生重复行。
func (r *Repo) AddMembers(ctx context.Context, convID int64, userIDs []int64) error {
	if len(userIDs) == 0 {
		return nil
	}
	var limit int
	var allMuted bool
	var memberCount int
	err := r.db.QueryRow(ctx, `SELECT member_limit, all_muted FROM groups WHERE conversation_id=$1`, convID).
		Scan(&limit, &allMuted)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if err := r.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM conversation_members WHERE conversation_id=$1 AND deleted=false`, convID).
		Scan(&memberCount); err != nil {
		return err
	}
	if memberCount+len(userIDs) > limit {
		return ErrMemberLimit
	}

	_, err = r.db.Exec(ctx, `
		INSERT INTO conversation_members (conversation_id, user_id, role)
		SELECT $1, uid, $2 FROM unnest($3::bigint[]) AS uid
		ON CONFLICT (conversation_id, user_id)
		DO UPDATE SET deleted = false, joined_at = now()`,
		convID, model.MemberRoleMember, userIDs)
	return err
}

// RemoveMember 将成员移出会话。采用软删除（deleted=true）而非物理删除，
// 保留历史已读位置/角色等数据，重新入群时可恢复。
func (r *Repo) RemoveMember(ctx context.Context, convID, userID int64) error {
	_, err := r.db.Exec(ctx,
		`UPDATE conversation_members SET deleted = true WHERE conversation_id=$1 AND user_id=$2`,
		convID, userID)
	return err
}

// MarkRead 在单个事务内完成三件事：
// 1) 推进成员的已读位置并清零未读数（GREATEST 保证序号只前进不回退，容忍乱序/重复上报）；
// 2) upsert 已读回执表 message_receipts，供对端查询"对方读到哪了"；
// 3) 清理该用户在此会话中已读序号之前的离线消息，避免重复推送。
func (r *Repo) MarkRead(ctx context.Context, convID, userID, seq int64) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		UPDATE conversation_members
		SET last_read_seq = GREATEST(last_read_seq, $3), unread_count = 0
		WHERE conversation_id=$1 AND user_id=$2`, convID, userID, seq); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO message_receipts (conversation_id, user_id, last_read_seq)
		VALUES ($1,$2,$3)
		ON CONFLICT (conversation_id, user_id)
		DO UPDATE SET last_read_seq = GREATEST(message_receipts.last_read_seq, EXCLUDED.last_read_seq),
		              updated_at = now()`, convID, userID, seq); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM offline_messages WHERE user_id=$1 AND conversation_id=$2 AND seq <= $3`,
		userID, convID, seq); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// UpdateLastMessage 更新会话的最后一条消息与时间（会话列表排序依据），
// 并用 GREATEST 推进 max_seq——序号由 Redis 分配时这里只是兜底校准，不能回退。
func (r *Repo) UpdateLastMessage(ctx context.Context, convID, msgID, seq int64, at time.Time) error {
	_, err := r.db.Exec(ctx, `
		UPDATE conversations SET last_msg_id=$2, last_msg_at=$3, max_seq=GREATEST(max_seq,$4)
		WHERE id=$1`, convID, msgID, at, seq)
	return err
}

// BumpUnreadExcept 给会话内除发送者外的所有有效成员未读数 +1。
func (r *Repo) BumpUnreadExcept(ctx context.Context, convID, exceptUser int64) error {
	_, err := r.db.Exec(ctx, `
		UPDATE conversation_members SET unread_count = unread_count + 1
		WHERE conversation_id=$1 AND user_id <> $2 AND deleted = false`, convID, exceptUser)
	return err
}

// InsertOffline 为离线用户登记一条待投递消息；ON CONFLICT DO NOTHING 保证
// 重复扇出（如多网关节点同时处理）不会产生重复的离线记录。
func (r *Repo) InsertOffline(ctx context.Context, userID, convID, msgID, seq int64) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO offline_messages (user_id, conversation_id, message_id, seq)
		VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`, userID, convID, msgID, seq)
	return err
}

// PullOffline 按登记时间正序拉取用户离线期间积压的消息（联查消息正文），
// 用户上线补拉后由 MarkRead 清理对应记录。
func (r *Repo) PullOffline(ctx context.Context, userID int64, limit int) ([]*model.Message, error) {
	rows, err := r.db.Query(ctx, `
		SELECT m.id, m.conversation_id, m.seq, m.sender_id, m.client_msg_id,
		       m.type, m.content, m.quote_msg_id, m.status, m.created_at
		FROM offline_messages o
		JOIN messages m ON m.id = o.message_id
		WHERE o.user_id = $1
		ORDER BY o.created_at ASC
		LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Message
	for rows.Next() {
		var m model.Message
		var content []byte
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Seq, &m.SenderID, &m.ClientMsgID,
			&m.Type, &content, &m.QuoteMsgID, &m.Status, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.Content = content
		out = append(out, &m)
	}
	return out, rows.Err()
}

// ListMessages 按 seq 游标增量拉取会话历史（升序、不含已撤回 status=3 的消息），
// limit 非法时回落到默认 50 条，上限 200 防止超大分页拖垮库。
func (r *Repo) ListMessages(ctx context.Context, convID, fromSeq int64, limit int) ([]*model.Message, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.db.Query(ctx, `
		SELECT id, conversation_id, seq, sender_id, client_msg_id,
		       type, content, quote_msg_id, status, created_at
		FROM messages
		WHERE conversation_id=$1 AND seq > $2 AND status <> 3
		ORDER BY seq ASC
		LIMIT $3`, convID, fromSeq, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Message
	for rows.Next() {
		var m model.Message
		var content []byte
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Seq, &m.SenderID, &m.ClientMsgID,
			&m.Type, &content, &m.QuoteMsgID, &m.Status, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.Content = content
		out = append(out, &m)
	}
	return out, rows.Err()
}

// RecallMessage 撤回消息：WHERE 中限定 sender_id=操作者且 status=1，
// 因此只有发送者本人能撤回、且只能撤回一次；RowsAffected=0 说明消息不存在、
// 非本人所发或已撤回，统一返回 ErrNotFound（不暴露具体原因）。
func (r *Repo) RecallMessage(ctx context.Context, convID, msgID, operator int64) error {
	ct, err := r.db.Exec(ctx, `
		UPDATE messages SET status = 2
		WHERE id=$1 AND conversation_id=$2 AND sender_id=$3 AND status=1`,
		msgID, convID, operator)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// -------- helpers --------
// 以下 deref* 函数用于把 LEFT JOIN 可能产生的 NULL 指针字段安全地解引用为零值。
func derefInt64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}
func derefInt16(p *int16) int16 {
	if p == nil {
		return 0
	}
	return *p
}
func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
func derefTime(p *time.Time) time.Time {
	if p == nil {
		return time.Time{}
	}
	return *p
}

var _ = fmt.Sprintf
