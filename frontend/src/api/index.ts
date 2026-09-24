/**
 * 前端 REST API 的集中定义层。
 *
 * 所有与 cmd/api（无状态 HTTP 服务）交互的业务接口都在这里收敛，
 * 组件/store 不直接操作 axios；每个方法负责拆开后端统一信封 Envelope
 * 并直接返回业务数据（data.data），让上层代码只关心领域对象。
 *
 * 实时消息收发不在此处，走 WebSocket 网关（cmd/gateway）。
 */
import http, { type Envelope } from './http'

/** 用户信息；status 为账号状态（封禁等），role 区分普通用户与管理员 */
export interface User {
  id: number
  username?: string
  nickname: string
  avatar_url?: string
  signature?: string
  status: number
  role: number
}

/**
 * 聊天消息。
 * seq 为会话内单调递增序号（历史拉取与已读游标的基准）；
 * client_msg_id 由发送方生成，用于发送幂等去重和本地乐观消息的 ack 关联；
 * type 区分消息类型（1 文本 / 2 图片 / 3 文件 / 4 系统）；
 * status 为消息状态（2 表示已撤回）。
 */
export interface Message {
  id: number
  conversation_id: number
  seq: number
  sender_id: number
  client_msg_id: string
  type: number
  content: any
  quote_msg_id?: number
  status: number
  created_at: string
}

/**
 * 会话成员关系（"我"在某个会话里的视角）：
 * last_read_seq 为已读游标，配合 max_seq 计算未读；
 * pinned/muted 为置顶与免打扰设置。
 */
export interface Member {
  conversation_id: number
  user_id: number
  role: number
  last_read_seq: number
  unread_count: number
  pinned: boolean
  muted: boolean
  joined_at: string
}

/**
 * 会话（列表项）。type 区分单聊/群聊；
 * peer 为单聊时对方的用户信息（列表直接展示昵称头像）；
 * member 携带"我"在该会话中的未读数等状态；
 * last_msg 与 max_seq 用于列表预览和排序。
 */
export interface Conversation {
  id: number
  type: number
  title?: string
  owner_id?: number
  last_msg_at?: string
  max_seq: number
  peer?: User
  member: Member
  last_msg?: Message
}

/** 登录/注册返回：用户信息 + 双令牌（访问令牌 + 刷新令牌） */
export interface AuthResult {
  user: User
  access_token: string
  refresh_token: string
}

/** 业务 API 集合：认证、用户、会话、历史消息、已读上报、附件上传 */
export const api = {
  async register(payload: {
    username?: string
    email?: string
    phone?: string
    password: string
    nickname?: string
    device_id?: string
  }) {
    const { data } = await http.post<Envelope<AuthResult>>('/auth/register', payload)
    return data.data!
  },

  async login(payload: { login: string; password: string; device_id?: string }) {
    const { data } = await http.post<Envelope<AuthResult>>('/auth/login', payload)
    return data.data!
  },

  async logout() {
    await http.post('/auth/logout')
  },

  async me() {
    const { data } = await http.get<Envelope<User>>('/users/me')
    return data.data!
  },

  async listConversations() {
    const { data } = await http.get<Envelope<{ conversations: Conversation[] }>>('/conversations')
    return data.data!.conversations
  },

  /** 发起/获取与指定用户的单聊会话（后端按双方用户对幂等复用已有会话） */
  async createDirect(userId: number) {
    const { data } = await http.post<Envelope<Conversation>>('/conversations/direct', { user_id: userId })
    return data.data!
  },

  async createGroup(name: string, memberIds: number[]) {
    const { data } = await http.post<Envelope<Conversation>>('/conversations/group', {
      name,
      member_ids: memberIds,
    })
    return data.data!
  },

  /** 按序号游标分页拉取历史消息（from_seq 之前的一页，用于向上翻历史） */
  async listMessages(conversationId: number, fromSeq = 0, limit = 50) {
    const { data } = await http.get<Envelope<{ messages: Message[] }>>(
      `/conversations/${conversationId}/messages`,
      { params: { from_seq: fromSeq, limit } },
    )
    return data.data!.messages
  },

  /** 上报已读游标，服务端据此清零未读数并同步给发送方 */
  async markRead(conversationId: number, lastReadSeq: number) {
    await http.post(`/conversations/${conversationId}/read`, { last_read_seq: lastReadSeq })
  },

  /** 上传附件（multipart 表单），返回可访问 URL 与附件元数据，随后作为消息内容发送 */
  async uploadAttachment(file: File) {
    const form = new FormData()
    form.append('file', file)
    const { data } = await http.post<Envelope<{ url: string; attachment: any }>>('/attachments', form, {
      headers: { 'Content-Type': 'multipart/form-data' },
    })
    return data.data!
  },
}