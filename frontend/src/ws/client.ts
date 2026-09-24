/**
 * WebSocket 客户端封装：负责与 cmd/gateway 长连接网关通信。
 *
 * 职责：
 * - 建立/维护 WebSocket 连接（URL query 携带 token 与 device_id 完成鉴权）；
 * - 心跳保活，断线后指数退避自动重连，重连前先尝试刷新 token；
 * - 断线期间的消息进入离线发送队列，重连成功后自动冲刷；
 * - 提供基于 client_msg_id 的发送-ACK 关联（sendWithAck），支撑发送方确认与超时失败；
 * - 帧（Frame）按 type 分发到注册的处理器。
 */
export type FrameHandler = (data: any) => void

interface Frame {
  t: string
  id?: string
  ts?: number
  data?: any
}

export interface ChatClientOptions {
  url: string
  token: string
  deviceId: string
  onTokenExpired?: () => Promise<string | null> // 返回新 token 或 null
}

const HEARTBEAT_MS = 25_000
const MAX_BACKOFF = 30_000
const ACK_TIMEOUT = 10_000

export class ChatClient {
  private ws?: WebSocket
  private opts: ChatClientOptions
  private retry = 0
  private heartbeatTimer?: number
  private reconnectTimer?: number
  private closedByUser = false
  private connecting = false

  private handlers = new Map<string, Set<FrameHandler>>()
  // 等待服务端 ACK 的发送：key 为 client_msg_id，超时未收到 ACK 则 reject
  private pendingAck = new Map<string, { resolve: (v: any) => void; reject: (e: any) => void; timer: number }>()
  // 断线期间的离线发送队列，重连成功后按序冲刷
  private sendQueue: string[] = []

  constructor(opts: ChatClientOptions) {
    this.opts = opts
  }

  /** 建立连接；已连接或连接中时直接返回，避免重复建连 */
  connect() {
    if (this.connecting || this.ws?.readyState === WebSocket.OPEN) return
    this.connecting = true
    this.closedByUser = false

    // 鉴权信息放在 URL query：浏览器原生 WebSocket 不支持自定义请求头
    const ws = new WebSocket(`${this.opts.url}?token=${encodeURIComponent(this.opts.token)}&device_id=${encodeURIComponent(this.opts.deviceId)}`)
    this.ws = ws

    ws.onopen = () => {
      this.connecting = false
      this.retry = 0
      this.startHeartbeat()
      // 冲刷发送队列
      while (this.sendQueue.length) {
        ws.send(this.sendQueue.shift()!)
      }
      this.emit('open', {})
    }

    ws.onmessage = (e) => this.onMessage(e.data)
    ws.onerror = () => { /* onclose 会接管 */ }
    ws.onclose = () => {
      this.connecting = false
      this.stopHeartbeat()
      this.emit('close', {})
      if (!this.closedByUser) this.scheduleReconnect()
    }
  }

  private onMessage(raw: string) {
    let frame: Frame
    try {
      frame = JSON.parse(raw)
    } catch {
      return
    }

    if (frame.t === 'message.ack' && frame.data?.client_msg_id) {
      // 按 client_msg_id 关联到 sendWithAck 挂起的 Promise，确认发送成功并清掉超时计时器
      const entry = this.pendingAck.get(frame.data.client_msg_id)
      if (entry) {
        clearTimeout(entry.timer)
        this.pendingAck.delete(frame.data.client_msg_id)
        entry.resolve(frame.data)
      }
    }

    this.emit(frame.t, frame.data)
  }

  /**
   * 普通发送：连接正常时直接发出；断线时进入离线队列（上限 200 条，超出丢弃），
   * 待重连成功后统一冲刷。适用于心跳、已读上报等允许丢失或延迟的帧。
   */
  send(type: string, data: any = {}): void {
    const frame = JSON.stringify({ t: type, id: crypto.randomUUID(), ts: Date.now(), data })
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(frame)
    } else {
      if (this.sendQueue.length < 200) this.sendQueue.push(frame)
    }
  }

  /**
   * 发送需要服务端 ACK 确认的消息（如 message.send）。
   * 以 clientMsgID 为键挂起 Promise：收到对应 message.ack 帧时 resolve，
   * 超过 timeoutMs 未收到则 reject，调用方据此更新本地消息的发送状态。
   */
  sendWithAck<T = any>(type: string, data: any, clientMsgID: string, timeoutMs = ACK_TIMEOUT): Promise<T> {
    return new Promise<T>((resolve, reject) => {
      const frame = JSON.stringify({ t: type, id: crypto.randomUUID(), ts: Date.now(), data })
      if (this.ws?.readyState !== WebSocket.OPEN) {
        reject(new Error('socket not open'))
        return
      }
      const timer = window.setTimeout(() => {
        this.pendingAck.delete(clientMsgID)
        reject(new Error('ack timeout'))
      }, timeoutMs)

      this.pendingAck.set(clientMsgID, { resolve, reject, timer })
      this.ws.send(frame)
    })
  }

  on(type: string, fn: FrameHandler): () => void {
    if (!this.handlers.has(type)) this.handlers.set(type, new Set())
    this.handlers.get(type)!.add(fn)
    return () => this.handlers.get(type)?.delete(fn)
  }

  private emit(type: string, data: any) {
    this.handlers.get(type)?.forEach((fn) => {
      try {
        fn(data)
      } catch (e) {
        console.error('[ws] handler error', type, e)
      }
    })
  }

  private startHeartbeat() {
    this.stopHeartbeat()
    this.heartbeatTimer = window.setInterval(() => this.send('ping'), HEARTBEAT_MS)
  }

  private stopHeartbeat() {
    if (this.heartbeatTimer) {
      clearInterval(this.heartbeatTimer)
      this.heartbeatTimer = undefined
    }
  }

  /**
   * 断线重连调度：指数退避（1s、2s、4s…封顶 30s）加随机抖动，避免大量客户端同时重连压垮网关。
   * 真正重连前先回调 onTokenExpired 尝试换新 token——断线很可能就是因为 access token 过期。
   */
  private scheduleReconnect() {
    if (this.reconnectTimer) return
    const base = Math.min(MAX_BACKOFF, 1000 * 2 ** this.retry)
    const jitter = Math.random() * 500
    this.retry++

    this.reconnectTimer = window.setTimeout(async () => {
      this.reconnectTimer = undefined
      if (this.opts.onTokenExpired) {
        const newToken = await this.opts.onTokenExpired()
        if (newToken) this.opts.token = newToken
      }
      this.connect()
    }, base + jitter)
  }

  isOpen(): boolean {
    return this.ws?.readyState === WebSocket.OPEN
  }

  /** 主动关闭（登出/切账号）：置 closedByUser 阻止自动重连，并把所有挂起的 ACK 置为失败 */
  close() {
    this.closedByUser = true
    this.stopHeartbeat()
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer)
      this.reconnectTimer = undefined
    }
    // 关闭后不可能再收到 ACK，逐个 reject 让调用方尽快进入失败态
    this.pendingAck.forEach((p) => {
      clearTimeout(p.timer)
      p.reject(new Error('client closed'))
    })
    this.pendingAck.clear()
    this.ws?.close(1000, 'client_close')
  }
}