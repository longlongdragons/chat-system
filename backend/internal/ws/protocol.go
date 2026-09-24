// Package ws 实现 WebSocket 网关的核心逻辑：客户端帧协议定义、本地连接
// 注册表（Hub）、连接生命周期管理（读写泵）以及基于 Redis Pub/Sub 的
// 跨网关节点广播投递。网关节点本身无状态，用户可能连接在任意节点上，
// 因此所有需要触达多节点的消息都统一走 chat.broadcast 通道扇出。
package ws

import (
	"encoding/json"
	"time"
)

// Frame 是客户端与服务端之间 WebSocket 通信的统一帧结构。
// T 为帧类型（如 message.send / message.ack / error），ID 是客户端请求的
// 关联 id，服务端回包（ack/error）会原样带回以便客户端做请求-响应匹配；
// Data 按帧类型承载不同 payload。
type Frame struct {
	T    string          `json:"t"`
	ID   string          `json:"id,omitempty"`
	TS   int64           `json:"ts,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
}

// marshalFrame 统一封装服务端下行帧：自动补时间戳；序列化失败时降级为
// 一个 error 帧，保证连接不因单次编码错误而中断。
func marshalFrame(t string, data any) []byte {
	b, err := json.Marshal(map[string]any{
		"t":    t,
		"ts":   time.Now().Unix(),
		"data": data,
	})
	if err != nil {
		return []byte(`{"t":"error","data":{"code":"marshal_error"}}`)
	}
	return b
}

// ---- 客户端 -> 服务端 ----
// 以下结构体是各上行帧类型对应的 payload 定义。

// sendPayload 对应 message.send：发送消息。ClientMsgID 是客户端生成的
// 幂等键，用于重试去重（服务端对同一 client_msg_id 只落库一次）。
type sendPayload struct {
	ConversationID int64           `json:"conversation_id"`
	ClientMsgID    string          `json:"client_msg_id"`
	Type           int16           `json:"type"`
	Content        json.RawMessage `json:"content"`
	QuoteMsgID     *int64          `json:"quote_msg_id,omitempty"`
}

// readPayload 对应 message.read：已读上报。LastReadSeq 是该会话内
// 单调递增的消息序号，语义为「序号 <= last_read_seq 的消息我都已读」。
type readPayload struct {
	ConversationID int64 `json:"conversation_id"`
	LastReadSeq    int64 `json:"last_read_seq"`
}

// recallPayload 对应 message.recall：撤回指定消息。
type recallPayload struct {
	ConversationID int64 `json:"conversation_id"`
	MessageID      int64 `json:"message_id"`
}

// syncPullPayload 对应 sync.pull：断线重连后按序号增量补拉消息，
// 拉取 (from_seq, ...] 区间的消息，limit 控制单批大小。
type syncPullPayload struct {
	ConversationID int64 `json:"conversation_id"`
	FromSeq        int64 `json:"from_seq"`
	Limit          int   `json:"limit"`
}

// typingPayload 对应 typing：输入中状态提示（不落库，仅实时转发给会话内其他成员）。
type typingPayload struct {
	ConversationID int64 `json:"conversation_id"`
}

// ---- 广播事件 ----

// BroadcastEvent 是发布到 Redis Pub/Sub 通道 chat.broadcast 的事件信封：
// 任意节点（含 API 服务）产生需要实时触达用户的事件时，带上目标用户列表
// 与帧类型广播出去，各网关节点收到后只投递给落在本节点上的目标连接。
type BroadcastEvent struct {
	TargetUserIDs []int64         `json:"target_user_ids"`
	FrameType     string          `json:"frame_type"`
	Data          json.RawMessage `json:"data"`
}
