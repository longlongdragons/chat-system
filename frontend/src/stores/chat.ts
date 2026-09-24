/**
 * 聊天状态核心（Pinia store）。
 *
 * 职责：
 * - 持有 ChatClient（WebSocket）生命周期：登录后 init 建连、退出时 teardown 清理；
 * - 维护会话列表、各会话消息列表、在线用户集合等 UI 状态；
 * - 处理网关推送的各类帧（新消息、ACK、已读、撤回、在线状态、离线/同步补发）；
 * - 发送消息采用「乐观本地回显 + ACK 确认」：先插入本地占位消息，
 *   收到服务端 ACK 后回填真实 id/seq，超时或失败则标记为 failed；
 * - 已读上报走 WS 并用 HTTP 兜底，保证断线时也能清除未读数。
 */
import { defineStore } from 'pinia'
import { api, type Conversation, type Message } from '@/api'
import { ChatClient } from '@/ws/client'
import { useAuthStore } from './auth'

const WS_URL = (import.meta.env.VITE_WS_URL as string) || 'ws://localhost:8081/ws'

/**
 * 本地消息：在服务端 Message 基础上附加发送状态字段。
 * _local 标记为本地占位消息（尚未被服务端确认），_status 驱动 UI 上的发送中/失败态展示。
 */
export interface LocalMessage extends Message {
  _status?: 'sending' | 'sent' | 'failed'
  _local?: boolean
}

export const useChatStore = defineStore('chat', {
  state: () => ({
    client: null as ChatClient | null,
    connected: false,
    conversations: [] as Conversation[],
    messages: {} as Record<number, LocalMessage[]>,
    activeConversationId: 0 as number,
    onlineUsers: new Set<number>(),
    unsubs: [] as Array<() => void>,
  }),

  getters: {
    activeConversation(state): Conversation | undefined {
      return state.conversations.find((c) => c.id === state.activeConversationId)
    },
    activeMessages(state): LocalMessage[] {
      return state.messages[state.activeConversationId] ?? []
    },
  },

  actions: {
    /** 登录成功后初始化：创建 ChatClient、注册帧处理器并发起连接 */
    async init() {
      const auth = useAuthStore()
      if (!auth.accessToken) return

      const client = new ChatClient({
        url: WS_URL,
        token: auth.accessToken,
        deviceId: auth.deviceId,
        onTokenExpired: async () => {
          // 通过 HTTP 层的 refresh 拦截器已经换过 token；这里读取最新值
          return auth.accessToken
        },
      })

      this.client = client
      this.bindEvents(client)

      client.on('open', () => {
        this.connected = true
        void this.loadConversations()
      })
      client.on('close', () => {
        this.connected = false
      })

      client.connect()
    },

    /**
     * 注册所有网关帧的处理器，返回的取消函数存入 unsubs，teardown 时统一注销，
     * 避免重复 init 造成同一帧被处理多次。
     */
    bindEvents(client: ChatClient) {
      this.unsubs.push(
        client.on('message.new', (msg: Message) => this.onNewMessage(msg)),
        client.on('message.ack', (ack: any) => this.onAck(ack)),
        client.on('message.read', (d: any) => this.onRead(d)),
        client.on('message.recall', (d: any) => this.onRecall(d)),
        client.on('presence', (d: any) => {
          if (d.status === 1) this.onlineUsers.add(d.user_id)
          else this.onlineUsers.delete(d.user_id)
        }),
        client.on('offline.batch', (d: any) => {
          (d.messages ?? []).forEach((m: Message) => this.onNewMessage(m))
        }),
        client.on('sync.batch', (d: any) => {
          (d.messages ?? []).forEach((m: Message) => this.onNewMessage(m))
        }),
        client.on('error', (d: any) => {
          console.warn('[ws] server error', d)
        }),
      )
    },

    /** 退出登录/重新初始化：注销全部帧订阅、关闭 WS，并清空所有聊天相关状态 */
    teardown() {
      this.unsubs.forEach((fn) => fn())
      this.unsubs = []
      this.client?.close()
      this.client = null
      this.connected = false
      this.conversations = []
      this.messages = {}
      this.activeConversationId = 0
      this.onlineUsers.clear()
    },

    async loadConversations() {
      const list = await api.listConversations()
      this.conversations = list
    },

    /** 打开会话：首次进入时拉取最近 50 条历史；有未读则以上一条消息 seq 上报已读 */
    async openConversation(convId: number) {
      this.activeConversationId = convId

      if (!this.messages[convId]) {
        const history = await api.listMessages(convId, 0, 50)
        this.messages[convId] = history as LocalMessage[]
      }

      const conv = this.conversations.find((c) => c.id === convId)
      if (conv && conv.member.unread_count > 0) {
        const lastSeq = this.maxSeq(convId)
        if (lastSeq > 0) await this.markRead(convId, lastSeq)
      }
    },

    maxSeq(convId: number): number {
      const list = this.messages[convId] ?? []
      return list.reduce((m, x) => Math.max(m, x.seq), 0)
    },

    /**
     * 收到新消息（含离线补发、增量同步）：去重后按 seq 插入对应会话，
     * 并维护会话列表的 last_msg / 未读数 / 置顶排序。
     */
    onNewMessage(msg: Message) {
      const list = this.messages[msg.conversation_id] ?? []
      if (list.some((m) => m.id === msg.id)) return // 去重

      // 服务端确认的消息到达后，替换掉同 client_msg_id 的本地占位，避免同一条消息显示两遍
      const idx = list.findIndex((m) => m._local && m.client_msg_id === msg.client_msg_id)
      if (idx >= 0) list.splice(idx, 1)

      list.push(msg)
      list.sort((a, b) => a.seq - b.seq)
      this.messages[msg.conversation_id] = list

      const conv = this.conversations.find((c) => c.id === msg.conversation_id)
      if (conv) {
        conv.last_msg = msg
        conv.last_msg_at = msg.created_at
        conv.max_seq = Math.max(conv.max_seq, msg.seq)

        const isActive = this.activeConversationId === msg.conversation_id
        const isMine = msg.sender_id === useAuthStore().user?.id
        // 正在查看该会话或消息是自己发的：不产生未读，直接上报已读；
        // 否则累加未读角标
        if (isActive || isMine) {
          void this.markRead(msg.conversation_id, msg.seq)
        } else {
          conv.member.unread_count += 1
        }
        // 置顶到列表顶部
        this.conversations = [conv, ...this.conversations.filter((c) => c.id !== conv.id)]
      } else {
        // 本地没有该会话（新会话的第一条消息）：整体刷新会话列表
        void this.loadConversations()
      }
    },

    /**
     * 发送 ACK：把本地占位消息回填为服务端正式分配的 id/seq/时间，
     * 状态从 sending 置为 sent，并按真实 seq 重新排序（占位消息原本排在最后）。
     */
    onAck(ack: any) {
      const list = this.messages[ack.conversation_id] ?? []
      const local = list.find((m) => m._local && m.client_msg_id === ack.client_msg_id)
      if (local) {
        local.id = ack.message_id
        local.seq = ack.seq
        local.created_at = ack.created_at
        local._status = 'sent'
        list.sort((a, b) => a.seq - b.seq)
      }
    },

    onRead(d: { conversation_id: number; user_id: number; last_read_seq: number }) {
      // 只关心自己的已读回执（多端同步场景）：推进已读游标并清零未读角标
      const conv = this.conversations.find((c) => c.id === d.conversation_id)
      if (conv && d.user_id === useAuthStore().user?.id) {
        conv.member.last_read_seq = Math.max(conv.member.last_read_seq, d.last_read_seq)
        conv.member.unread_count = 0
      }
    },

    onRecall(d: { conversation_id: number; message_id: number }) {
      const list = this.messages[d.conversation_id] ?? []
      const msg = list.find((m) => m.id === d.message_id)
      if (msg) msg.status = 2
    },

    async sendText(convId: number, text: string) {
      const content = { text }
      await this.sendMessage(convId, 1, content)
    },

    async sendImage(convId: number, url: string, width: number, height: number) {
      await this.sendMessage(convId, 2, { url, width, height })
    },

    /**
     * 发送消息的统一入口：先生成 client_msg_id 并插入本地占位消息（乐观回显，
     * seq 置为最大值使其临时排在列表末尾），再经 WS 发送并等待 ACK；
     * ACK 超时/失败时把占位消息标记为 failed，供 UI 展示重发入口。
     */
    async sendMessage(convId: number, type: number, content: any) {
      if (!this.client) return
      const clientMsgId = crypto.randomUUID()

      const local: LocalMessage = {
        id: 0,
        conversation_id: convId,
        seq: Number.MAX_SAFE_INTEGER, // 本地排到最后
        sender_id: useAuthStore().user?.id ?? 0,
        client_msg_id: clientMsgId,
        type,
        content,
        status: 1,
        created_at: new Date().toISOString(),
        _status: 'sending',
        _local: true,
      }
      const list = this.messages[convId] ?? []
      list.push(local)
      this.messages[convId] = list

      try {
        await this.client.sendWithAck(
          'message.send',
          { conversation_id: convId, client_msg_id: clientMsgId, type, content },
          clientMsgId,
        )
      } catch (e) {
        local._status = 'failed'
        console.error('[chat] send failed', e)
      }
    },

    /**
     * 上报已读位置：已读游标只前进不后退（本地已 >= seq 时跳过）；
     * WS 与 HTTP 双通道上报，保证断线期间也能在服务端清除未读数。
     */
    async markRead(convId: number, seq: number) {
      if (!this.client) return
      const conv = this.conversations.find((c) => c.id === convId)
      if (conv && conv.member.last_read_seq >= seq) return
      this.client.send('message.read', { conversation_id: convId, last_read_seq: seq })
      if (conv) {
        conv.member.last_read_seq = seq
        conv.member.unread_count = 0
      }
      // HTTP 兜底（WS 断线时也能清未读）
      try {
        await api.markRead(convId, seq)
      } catch { /* ignore */ }
    },

    /**
     * 向上翻页拉取更早的历史：以当前最小 seq 为锚点向前取 50 条，
     * 与本地列表合并后按消息 id 去重（本地占位消息没有 id，用 client_msg_id 的时间戳兜底区分），
     * 最后按 seq 排序。
     */
    async pullHistory(convId: number) {
      const list = this.messages[convId] ?? []
      const minSeq = list.reduce((m, x) => Math.min(m, x.seq), Number.MAX_SAFE_INTEGER)
      const from = minSeq === Number.MAX_SAFE_INTEGER ? 0 : Math.max(0, minSeq - 50)
      const older = await api.listMessages(convId, from, 50)
      const merged = [...(older as LocalMessage[]), ...list]
      const dedup = new Map<number, LocalMessage>()
      merged.forEach((m) => dedup.set(m.id || -Date.parse(m.client_msg_id), m))
      this.messages[convId] = [...dedup.values()].sort((a, b) => a.seq - b.seq)
    },
  },
})