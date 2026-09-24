// Package model 定义聊天系统的核心领域模型（用户、会话、成员、消息、附件）
// 以及各业务枚举常量，是 API 服务与 WebSocket 网关共享的数据契约。
package model

import (
	"encoding/json"
	"time"
)

// -------- 用户 --------

// User 对应 users 表，是账号主体。
// Username/Email/Phone 均设为可空指针：注册时三者可只填其一（见 user.Create），
// PasswordHash 与 TokenVersion 不参与 JSON 序列化，避免泄露到前端。
type User struct {
	ID           int64     `json:"id"`
	Username     *string   `json:"username,omitempty"`
	Email        *string   `json:"email,omitempty"`
	Phone        *string   `json:"phone,omitempty"`
	PasswordHash string    `json:"-"`
	Nickname     string    `json:"nickname"`
	AvatarURL    *string   `json:"avatar_url,omitempty"`
	Signature    *string   `json:"signature,omitempty"`
	Status       int16     `json:"status"`
	Role         int16     `json:"role"`
	TokenVersion int       `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// -------- 会话 --------

// Conversation 对应 conversations 表，表示一个会话（单聊/群聊/客服）。
// MaxSeq 是该会话内已分配的最大消息序号，发消息时在其上递增，
// 配合成员的 LastReadSeq 即可算出未读数；LastMsgID/LastMsgAt 冗余存储，用于会话列表快速排序展示。
type Conversation struct {
	ID        int64      `json:"id"`
	Type      int16      `json:"type"` // 1单聊 2群聊 3客服
	Title     *string    `json:"title,omitempty"`
	OwnerID   *int64     `json:"owner_id,omitempty"`
	LastMsgID *int64     `json:"last_msg_id,omitempty"`
	LastMsgAt *time.Time `json:"last_msg_at,omitempty"`
	MaxSeq    int64      `json:"max_seq"`
	Status    int16      `json:"status"`
	CreatedAt time.Time  `json:"created_at"`
}

// Member 对应 conversation_members 表，表示用户在某会话中的成员关系。
// LastReadSeq 配合会话的 MaxSeq 计算未读；Pinned/Muted/MuteUntil 驱动置顶与免打扰；
// Deleted 是用户维度的"删除会话"软标记，只影响本人会话列表的可见性。
type Member struct {
	ConversationID int64      `json:"conversation_id"`
	UserID         int64      `json:"user_id"`
	Role           int16      `json:"role"`
	LastReadSeq    int64      `json:"last_read_seq"`
	UnreadCount    int        `json:"unread_count"`
	Pinned         bool       `json:"pinned"`
	Muted          bool       `json:"muted"`
	Deleted        bool       `json:"deleted"`
	MuteUntil      *time.Time `json:"mute_until,omitempty"`
	JoinedAt       time.Time  `json:"joined_at"`
}

// 会话列表视图
// ConversationView 是面向前端的会话列表聚合视图：在会话本体上附加
// 单聊对端资料、当前用户的成员状态（未读/置顶/免打扰）和最后一条消息摘要。
type ConversationView struct {
	Conversation
	Peer    *User    `json:"peer,omitempty"` // 单聊对端
	Member  Member   `json:"member"`         // 当前用户成员信息
	LastMsg *Message `json:"last_msg,omitempty"`
}

// -------- 消息 --------

// Message 对应 messages 表，表示一条聊天消息。
// Seq 是会话内单调递增的序号，用于消息排序、未读计算与断线补拉；
// ClientMsgID 由客户端生成，配合唯一约束实现重发去重（幂等）；
// Content 为 JSON（文本/图片/文件等载荷），用 json.RawMessage 延迟解析，模型层不关心具体形态。
type Message struct {
	ID             int64           `json:"id"`
	ConversationID int64           `json:"conversation_id"`
	Seq            int64           `json:"seq"`
	SenderID       int64           `json:"sender_id"`
	ClientMsgID    string          `json:"client_msg_id"`
	Type           int16           `json:"type"` // 1文本 2图片 3文件 4系统 5引用
	Content        json.RawMessage `json:"content"`
	QuoteMsgID     *int64          `json:"quote_msg_id,omitempty"`
	Status         int16           `json:"status"`
	CreatedAt      time.Time       `json:"created_at"`
}

// -------- 附件 --------

// Attachment 对应 attachments 表，表示一条上传文件记录。
// ObjectKey 是文件在对象存储/磁盘上的定位键；Status 标记上传完成或已清理等生命周期状态。
type Attachment struct {
	ID         int64     `json:"id"`
	UploaderID int64     `json:"uploader_id"`
	ObjectKey  string    `json:"object_key"`
	FileName   string    `json:"file_name"`
	MimeType   string    `json:"mime_type"`
	SizeBytes  int64     `json:"size_bytes"`
	Status     int16     `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
}

// 业务枚举常量：消息类型、会话类型、用户状态、全局角色、会话内成员角色。
// 角色值刻意留出间隔（10/20/30…），方便日后插入中间层级；比较时可用大小判断权限高低。
const (
	MsgTypeText   int16 = 1
	MsgTypeImage  int16 = 2
	MsgTypeFile   int16 = 3
	MsgTypeSystem int16 = 4
	MsgTypeQuote  int16 = 5

	ConvTypeDirect int16 = 1
	ConvTypeGroup  int16 = 2
	ConvTypeCS     int16 = 3

	UserStatusNormal  int16 = 1
	UserStatusBanned  int16 = 2
	UserStatusDeleted int16 = 3

	RoleUser    int16 = 10
	RoleSupport int16 = 20
	RoleAuditor int16 = 30
	RoleAdmin   int16 = 40
	RoleSuper   int16 = 50

	MemberRoleMember int16 = 0
	MemberRoleAdmin  int16 = 1
	MemberRoleOwner  int16 = 2
)
