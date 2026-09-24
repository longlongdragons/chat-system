package ws

import (
	"encoding/json"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// 连接读写参数：
//   - pongWait：读超时。客户端必须在此窗口内回复 pong（或发来任意帧），否则判定掉线；
//   - pingPeriod：服务端主动发 ping 的周期，须明显小于 pongWait 才能让保活生效；
//   - maxMessageSize：单帧上限，防止恶意超大帧耗尽内存；
//   - sendBufferSize：每连接下行缓冲，配合慢消费者保护策略使用。
const (
	writeWait      = 10 * time.Second
	pongWait       = 75 * time.Second
	pingPeriod     = 25 * time.Second
	maxMessageSize = 128 * 1024
	sendBufferSize = 256
)

// Conn 封装一条已升级的 WebSocket 连接及其元信息（所属用户、设备、所在节点）。
// 下行数据统一经 sendCh 缓冲，由专属 writePump 协程串行写到底层连接，
// 避免多协程并发写 WebSocket（gorilla/websocket 不允许并发写）。
type Conn struct {
	ID       string
	UserID   int64
	DeviceID string
	ServerID string

	ws        *websocket.Conn
	sendCh    chan []byte
	closeOnce sync.Once // 保证 Close 只执行一次（sendCh 只能 close 一回）
	closed    atomic.Bool

	hub *Hub
}

func newConn(wsConn *websocket.Conn, id string, userID int64, deviceID, serverID string, hub *Hub) *Conn {
	return &Conn{
		ID:       id,
		UserID:   userID,
		DeviceID: deviceID,
		ServerID: serverID,
		ws:       wsConn,
		sendCh:   make(chan []byte, sendBufferSize),
		hub:      hub,
	}
}

// SendFrame 构造下行帧并投入发送缓冲。缓冲满说明客户端消费过慢，
// 直接关闭连接（慢消费者保护），由客户端重连后通过 sync.pull 补齐。
func (c *Conn) SendFrame(t string, data any) {
	select {
	case c.sendCh <- marshalFrame(t, data):
	default:
		log.Printf("[ws] send buffer full, closing conn=%s user=%d", c.ID, c.UserID)
		c.Close("slow_consumer")
	}
}

// SendRaw 投递已序列化好的帧（跨节点广播场景下帧只编码一次，逐连接复用）。
func (c *Conn) SendRaw(b []byte) {
	select {
	case c.sendCh <- b:
	default:
		c.Close("slow_consumer")
	}
}

// SendError 回错误帧；refID 带回触发错误的请求帧 id，便于客户端做关联。
func (c *Conn) SendError(code, msg, refID string) {
	c.SendFrame("error", map[string]any{"code": code, "message": msg, "ref_id": refID})
}

// Close 幂等关闭连接：先尽力把 kick 帧（携带关闭原因）塞进缓冲让客户端
// 感知原因，再关闭 sendCh 通知 writePump 退出并回收底层连接。
func (c *Conn) Close(reason string) {
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		select {
		case c.sendCh <- marshalFrame("kick", map[string]string{"reason": reason}):
		default:
		}
		close(c.sendCh)
	})
}

// readPump 读循环：在 HTTP 处理器 goroutine 中阻塞运行，逐帧读取并分发。
// 循环退出（客户端断开/读超时/协议错误）即连接生命周期结束，
// defer 中注销连接并关闭底层 socket。
func (c *Conn) readPump(h *Handler) {
	defer func() {
		c.hub.Unregister(c)
		_ = c.ws.Close()
	}()

	c.ws.SetReadLimit(maxMessageSize)
	_ = c.ws.SetReadDeadline(time.Now().Add(pongWait))
	// 收到 pong 说明对端存活：顺延读超时，并顺手刷新 Redis 在线状态 TTL。
	c.ws.SetPongHandler(func(string) error {
		_ = c.ws.SetReadDeadline(time.Now().Add(pongWait))
		c.hub.presence.Heartbeat(c.hub.ctx, c.UserID)
		return nil
	})

	for {
		_, raw, err := c.ws.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("[ws] read error user=%d: %v", c.UserID, err)
			}
			return
		}
		var frame Frame
		if err := json.Unmarshal(raw, &frame); err != nil {
			// 单帧格式错误只回错误帧、不断连，保持长连接健壮性。
			c.SendError("bad_frame", "invalid json", "")
			continue
		}
		h.Dispatch(c, &frame)
	}
}

// writePump 写循环：独占底层连接的写权限，串行发送 sendCh 中的下行帧，
// 并按 pingPeriod 周期性发送协议层 ping 保活；sendCh 关闭或写失败即退出。
func (c *Conn) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.ws.Close()
	}()

	for {
		select {
		case msg, ok := <-c.sendCh:
			_ = c.ws.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = c.ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				return
			}
			if err := c.ws.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.ws.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.ws.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
