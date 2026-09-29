package ws

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/example/chat/internal/bus"
	"github.com/example/chat/internal/conversation"
	"github.com/example/chat/internal/presence"
	"github.com/google/uuid"
)

// Hub 是本网关节点的本地连接注册表：维护「用户 -> 该用户全部连接」与
// 「连接 id -> 连接」两级索引（支持同一用户多端同时在线），并负责把消息
// 推送到本地连接，以及通过 Redis Pub/Sub 与其他网关节点互投广播事件。
//
// Hub 同时是消息服务的广播出口（实现了 message.Service 依赖的 Broadcaster
// 接口），REST 侧与 WS 侧产生的实时事件都经它扇出。
type Hub struct {
	ctx      context.Context
	serverID string
	bus      bus.Bus
	presence *presence.Store
	convs    *conversation.Repo // 会话仓储：presence 事件扇出前查询共同会话成员

	maxConns        int // 本节点总连接数上限（护栏），<=0 表示不限制
	maxConnsPerUser int // 单用户连接数上限（多端护栏），<=0 表示不限制

	mu    sync.RWMutex
	users map[int64]map[string]*Conn // userID -> (connID -> Conn)，支持多端
	conns map[string]*Conn           // connID -> Conn，本节点全部连接
}

// ErrTooManyConnections 是连接数护栏触发时 Register 返回的哨兵错误，
// 由连接处理器映射为 too_many_connections 错误帧告知客户端。
var ErrTooManyConnections = errors.New("too many connections")

// HubConfig 是 Hub 的可选依赖与运行参数集合，零值即安全可用（各能力降级/不限制）。
type HubConfig struct {
	// Convs 会话仓储：presence 上线/离线广播前用它查询「与本人有共同会话的成员」，
	// 实现精准扇出；为 nil 时退化为只推送给状态变化者本人。
	Convs *conversation.Repo
	// MaxConns 是本节点允许同时持有的最大连接数：打到上限说明节点容量已饱和，
	// 新连接直接拒绝（客户端重连到其他节点），避免内存/文件句柄被缓慢耗尽。
	// <=0 表示不限制。
	MaxConns int
	// MaxConnsPerUser 是单用户允许同时在线的最大连接数：限制异常客户端
	// 无限重连/多开挤占节点容量。<=0 表示不限制。
	MaxConnsPerUser int
}

// NewHub 创建本地连接注册表。serverID 用于区分网关节点（连接 id 前缀及
// 在线状态归属），bus 用于跨节点广播，pr 用于维护 Redis 在线状态。
func NewHub(ctx context.Context, serverID string, b bus.Bus, pr *presence.Store, cfg HubConfig) *Hub {
	return &Hub{
		ctx:             ctx,
		serverID:        serverID,
		bus:             b,
		presence:        pr,
		convs:           cfg.Convs,
		maxConns:        cfg.MaxConns,
		maxConnsPerUser: cfg.MaxConnsPerUser,
		users:           make(map[int64]map[string]*Conn),
		conns:           make(map[string]*Conn),
	}
}

// ServerID 返回本网关节点的标识。
func (h *Hub) ServerID() string { return h.serverID }

// newConnID 生成全局唯一的连接 id，带节点前缀便于定位连接落在哪个网关实例上。
func (h *Hub) newConnID() string { return h.serverID + ":" + uuid.NewString() }

// Register 注册连接，返回是否由本实例触发「用户上线」。
// 注册前先过连接数护栏：节点总连接数或该用户连接数任一达到上限时，
// 返回 ErrTooManyConnections 拒绝注册（连接不进入注册表，也无需注销）。
func (h *Hub) Register(c *Conn) (firstOnline bool, err error) {
	h.mu.Lock()
	// 护栏校验与注册必须在同一把锁内完成，否则并发注册可能同时越过上限。
	if h.maxConns > 0 && len(h.conns) >= h.maxConns {
		h.mu.Unlock()
		return false, ErrTooManyConnections
	}
	m, ok := h.users[c.UserID]
	if h.maxConnsPerUser > 0 && ok && len(m) >= h.maxConnsPerUser {
		h.mu.Unlock()
		return false, ErrTooManyConnections
	}
	if !ok {
		m = make(map[string]*Conn)
		h.users[c.UserID] = m
	}
	m[c.ID] = c
	h.conns[c.ID] = c
	h.mu.Unlock()

	// Redis 在线计数 +1；仅当这是该用户全平台第一条连接（total==1）时才算
	// 「上线」，用于决定是否需要广播 presence 事件。
	// 在线状态写失败不阻断连接建立：实时聊天可用性优先，状态可后续自愈。
	total, err := h.presence.Add(h.ctx, c.UserID, h.serverID)
	if err != nil {
		log.Printf("[ws] presence add failed: %v", err)
		return false, nil
	}
	return total == 1, nil
}

// Unregister 注销连接，返回是否为最后一个连接（用户彻底离线）
func (h *Hub) Unregister(c *Conn) (lastOffline bool) {
	h.mu.Lock()
	if m, ok := h.users[c.UserID]; ok {
		delete(m, c.ID)
		if len(m) == 0 {
			delete(h.users, c.UserID)
		}
	}
	delete(h.conns, c.ID)
	h.mu.Unlock()

	// Redis 在线计数 -1；归零才代表用户全部端都离线。
	total, err := h.presence.Remove(h.ctx, c.UserID, h.serverID)
	if err != nil {
		log.Printf("[ws] presence remove failed: %v", err)
		return false
	}
	return total == 0
}

// PushToUser 本地推送：把帧投递给该用户落在本节点上的所有连接（多端同时收），
// 返回成功投递的连接数。
func (h *Hub) PushToUser(userID int64, payload []byte) int {
	h.mu.RLock()
	// 先在锁内拷贝连接快照再释放锁，避免推送期间持锁阻塞注册/注销。
	conns := make([]*Conn, 0, len(h.users[userID]))
	for _, c := range h.users[userID] {
		conns = append(conns, c)
	}
	h.mu.RUnlock()

	n := 0
	for _, c := range conns {
		if c.closed.Load() {
			continue
		}
		select {
		case c.sendCh <- payload:
			n++
		default:
			// 慢消费者保护：该连接发送缓冲已满（客户端长时间不读取），
			// 异步关闭连接以免一条坏连接拖垮整个节点的推送。
			go c.Close("slow_consumer")
		}
	}
	return n
}

// LocalConnCount 返回本节点当前连接总数（供健康检查与监控指标使用）。
func (h *Hub) LocalConnCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.conns)
}

// LocalUserCount 返回本节点当前在线用户数。
func (h *Hub) LocalUserCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.users)
}

// BroadcastToUsers 通过 Redis Pub/Sub 广播到所有网关实例：
// 目标用户的连接可能落在任意节点上，因此不区分本地/远端，统一发布事件，
// 由各节点的订阅回调各自筛选本节点持有的连接投递。
func (h *Hub) BroadcastToUsers(ctx context.Context, userIDs []int64, frameType string, data any) error {
	if len(userIDs) == 0 {
		return nil
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	evt := BroadcastEvent{
		TargetUserIDs: userIDs,
		FrameType:     frameType,
		Data:          payload,
	}
	raw, err := json.Marshal(evt)
	if err != nil {
		return err
	}
	return h.bus.Publish(ctx, bus.ChannelBroadcast, raw)
}

// StartSubscriber 订阅跨实例广播：启动后常驻消费 chat.broadcast 通道，
// 把其他节点（或 API 服务）发布的事件推送给本节点上的目标用户连接。
func (h *Hub) StartSubscriber(ctx context.Context) error {
	return h.bus.Subscribe(ctx, bus.ChannelBroadcast, func(payload []byte) {
		var evt BroadcastEvent
		if err := json.Unmarshal(payload, &evt); err != nil {
			return
		}
		// 每个目标用户独立投递；不在本节点的用户 PushToUser 自然为空操作。
		frame := marshalFrame(evt.FrameType, json.RawMessage(evt.Data))
		for _, uid := range evt.TargetUserIDs {
			h.PushToUser(uid, frame)
		}
	})
}

// BroadcastPresence 广播在线状态变化（上线/下线事件）。
// 扇出目标 = 状态变化者本人（多端同步自己的状态）+ 与其共处至少一个有效会话的
// 其他成员（单聊对端、群友），这些人才需要感知该用户的在线状态变化。
// 该函数运行在连接注册/注销的热路径上：查库带 2 秒超时预算，失败（或未注入
// 会话仓储）时降级为只推本人，绝不因 presence 扇出阻断连接的建立与断开。
func (h *Hub) BroadcastPresence(ctx context.Context, userID int64, online bool) {
	status := 0
	if online {
		status = 1
	}
	// 本人始终在目标集合内：既兜底了查库失败的降级场景，也让该用户的
	// 其他在线端能同步到本端的状态变化。
	targets := []int64{userID}
	if h.convs != nil {
		qctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		peers, err := h.convs.PeerIDsOfUser(qctx, userID)
		cancel()
		if err != nil {
			log.Printf("[ws] presence peers lookup failed user=%d, fallback to self: %v", userID, err)
		} else {
			targets = append(targets, peers...)
		}
	}
	if err := h.BroadcastToUsers(ctx, targets, "presence", map[string]any{
		"user_id": userID,
		"status":  status,
	}); err != nil {
		log.Printf("[ws] presence broadcast failed user=%d online=%v: %v", userID, online, err)
	}
}

// Shutdown 优雅停机：向本节点全部连接发送 kick 帧并关闭，让客户端
// 感知停机原因后主动重连到其他网关节点。
func (h *Hub) Shutdown(reason string) {
	h.mu.RLock()
	// 拷贝快照后逐个关闭，避免持锁期间触发连接的注销回调造成死锁。
	conns := make([]*Conn, 0, len(h.conns))
	for _, c := range h.conns {
		conns = append(conns, c)
	}
	h.mu.RUnlock()

	for _, c := range conns {
		c.Close(reason)
	}
}
