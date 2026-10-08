<template>
  <div class="chat-layout">
    <!-- 环境光斑背景层：三个大模糊渐变球缓慢漂移（纯装饰，pointer-events 关闭） -->
    <div class="bg-fx" aria-hidden="true"><i class="blob b1"></i><i class="blob b2"></i><i class="blob b3"></i></div>

    <!-- ===== 第 1 栏：图标导航栏（Discord 风格深色窄栏） ===== -->
    <nav class="rail">
      <!-- 我的头像：右下角叠连接状态点（绿=已连接 / 黄=重连中） -->
      <div
        class="rail-me"
        :data-tip="`${auth.user?.nickname ?? ''}（${chat.connected ? '已连接' : '重连中'}）`"
        :aria-label="`${auth.user?.nickname ?? ''}（${chat.connected ? '已连接' : '重连中'}）`"
      >
        <div class="avatar me-avatar">{{ initials(auth.user?.nickname) }}</div>
        <i :class="['status-dot', { on: chat.connected }]"></i>
      </div>

      <!-- 分类导航：消息 / 单聊 / 群聊 / 联系人 -->
      <div class="rail-nav">
        <button :class="['rail-btn', { on: category === 'all' }]" data-tip="消息" aria-label="消息" @click="selectCategory('all')">
          <svg viewBox="0 0 24 24" width="21" height="21" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
            <path d="M21 11.5a8.5 8.5 0 0 1-8.5 8.5H8l-4.3 3.1a.6.6 0 0 1-1-.5V11.5A8.5 8.5 0 0 1 11.2 3h1.3a8.5 8.5 0 0 1 8.5 8.5Z" />
          </svg>
          <span v-if="unreadTotal > 0" class="rail-badge">{{ unreadTotal > 99 ? '99+' : unreadTotal }}</span>
        </button>

        <button :class="['rail-btn', { on: category === 'direct' }]" data-tip="单聊" aria-label="单聊" @click="selectCategory('direct')">
          <svg viewBox="0 0 24 24" width="21" height="21" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="12" cy="8" r="4" />
            <path d="M4.5 20.5a7.5 7.5 0 0 1 15 0" />
          </svg>
          <span v-if="unreadDirect > 0" class="rail-badge">{{ unreadDirect > 99 ? '99+' : unreadDirect }}</span>
        </button>

        <button :class="['rail-btn', { on: category === 'group' }]" data-tip="群聊" aria-label="群聊" @click="selectCategory('group')">
          <svg viewBox="0 0 24 24" width="21" height="21" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="9" cy="8.5" r="3.4" />
            <path d="M2.8 19.5a6.2 6.2 0 0 1 12.4 0" />
            <path d="M15.5 5.6a3.4 3.4 0 1 1 1.6 6.4" />
            <path d="M17.6 13.7a6.2 6.2 0 0 1 3.6 5.8" />
          </svg>
          <span v-if="unreadGroup > 0" class="rail-badge">{{ unreadGroup > 99 ? '99+' : unreadGroup }}</span>
        </button>

        <button :class="['rail-btn', { on: category === 'contacts' }]" data-tip="联系人" aria-label="联系人" @click="selectCategory('contacts')">
          <svg viewBox="0 0 24 24" width="21" height="21" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
            <rect x="4" y="3" width="16" height="18" rx="2.5" />
            <circle cx="12" cy="10" r="2.6" />
            <path d="M7.8 16.6a4.4 4.4 0 0 1 8.4 0" />
          </svg>
        </button>
      </div>

      <!-- 底部：主题切换 + 退出 -->
      <div class="rail-bottom">
        <ThemeToggle />
        <button class="rail-btn" data-tip="退出登录" aria-label="退出登录" @click="logout">
          <svg viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
            <path d="M9 21H6a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h3" />
            <path d="m16 17 5-5-5-5" />
            <path d="M21 12H9" />
          </svg>
        </button>
      </div>
    </nav>

    <!-- ===== 第 2 栏：分类列表栏 ===== -->
    <aside class="panel">
      <header class="panel-header">
        <strong class="panel-title">{{ panelTitle }}</strong>
        <!-- 搜索框：仅前端过滤当前分类列表，按昵称/标题匹配 -->
        <div class="search-box">
          <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
            <circle cx="11" cy="11" r="7" />
            <path d="m20 20-3.2-3.2" />
          </svg>
          <input v-model.trim="searchText" placeholder="搜索" />
        </div>
      </header>

      <!-- 断线时顶部横幅提示（连接状态在窄栏里只有一个状态点，这里给出文字说明） -->
      <div v-if="!chat.connected" class="conn-banner"><i class="dot"></i>连接已断开，正在重连…</div>

      <!-- 分类快捷操作：发起单聊/群聊从旧侧栏挪到对应分类面板顶部 -->
      <div v-if="category === 'direct'" class="panel-actions">
        <div class="new-chat">
          <input v-model.trim="peerIdInput" placeholder="对方用户 ID" @keyup.enter="startDirect" />
          <button class="ghost-btn" @click="startDirect">发起</button>
        </div>
      </div>
      <div v-else-if="category === 'group'" class="panel-actions">
        <div class="new-group">
          <input v-model.trim="groupNameInput" placeholder="群名称" @keyup.enter="startGroup" />
          <div class="row2">
            <input
              v-model.trim="groupMembersInput"
              placeholder="成员 ID，逗号分隔，如 1,2"
              @keyup.enter="startGroup"
            />
            <button class="ghost-btn" @click="startGroup">发起</button>
          </div>
        </div>
      </div>

      <!-- 联系人分类：只读列表，点击即开聊 -->
      <ul v-if="category === 'contacts'" class="conv-list">
        <li v-for="u in filteredContacts" :key="u.id" @click="openContact(u.id)">
          <div class="avatar" :style="avatarStyle(u.id)">{{ initials(u.nickname || u.username) }}</div>
          <div class="body">
            <div class="row">
              <strong>{{ u.nickname || u.username || `用户 ${u.id}` }}</strong>
            </div>
            <div class="row">
              <span :class="['contact-status', { on: isOnline(u.id) }]">
                <i class="dot"></i>{{ isOnline(u.id) ? '在线' : '离线' }}
              </span>
            </div>
          </div>
        </li>
        <li v-if="filteredContacts.length === 0" class="empty">
          {{ searchText ? '没有匹配的联系人' : '暂无联系人，去发起单聊吧' }}
        </li>
      </ul>

      <!-- 会话列表（消息 / 单聊 / 群聊分类共用，按分类与搜索词过滤） -->
      <ul v-else class="conv-list">
        <li
          v-for="c in filteredConversations"
          :key="c.id"
          :class="{ active: c.id === chat.activeConversationId }"
          @click="chat.openConversation(c.id)"
        >
          <div class="avatar" :style="avatarStyle(c.peer?.id ?? c.id)">
            {{ initials(c.peer?.nickname || c.title || `#${c.id}`) }}
          </div>
          <div class="body">
            <div class="row">
              <strong>{{ c.peer?.nickname || c.title || `会话 ${c.id}` }}</strong>
              <small>{{ fmtTime(c.last_msg_at) }}</small>
            </div>
            <div class="row">
              <!-- 对端正在输入时，预览位替换为打字提示 -->
              <span v-if="convTyping(c.id)" class="typing-preview">正在输入…</span>
              <span v-else class="preview">{{ preview(c.last_msg) }}</span>
              <span v-if="c.member.unread_count > 0" class="badge">
                {{ c.member.unread_count > 99 ? '99+' : c.member.unread_count }}
              </span>
            </div>
          </div>
        </li>
        <li v-if="filteredConversations.length === 0" class="empty">{{ convEmptyText }}</li>
      </ul>
    </aside>

    <!-- ===== 第 3 栏：聊天主区 ===== -->
    <main class="main">
      <template v-if="chat.activeConversationId">
        <header class="chat-header">
          <div class="title-wrap">
            <strong>
              {{ chat.activeConversation?.peer?.nickname || chat.activeConversation?.title || '会话' }}
            </strong>
            <small class="conv-id">#{{ chat.activeConversationId }}</small>
          </div>
          <!-- 对端正在此会话输入时，头部展示"xxx 正在输入"（带跳动圆点） -->
          <transition name="fade">
            <span v-if="typingText" class="typing-hint">
              {{ typingText }}<i class="tdot"></i><i class="tdot"></i><i class="tdot"></i>
            </span>
          </transition>
        </header>

        <div class="messages-wrap">
          <div ref="scrollEl" class="messages" @scroll="onScroll">
          <!-- 向上翻页拉历史时的顶部加载指示 -->
          <div v-if="historyLoading" class="history-tip">
            <span class="spinner"></span>加载历史消息…
          </div>
          <div v-else-if="historyDoneSet.has(chat.activeConversationId)" class="history-tip done">
            已经到顶啦
          </div>

          <template v-for="row in msgRows" :key="row.msg.id || row.msg.client_msg_id">
            <!-- 相邻消息间隔超过 10 分钟时插入时间分隔条 -->
            <div v-if="row.divider" class="time-divider"><span>{{ row.divider }}</span></div>

            <div :class="['msg', { mine: row.msg.sender_id === auth.user?.id }]">
              <div class="avatar" :style="avatarStyle(row.msg.sender_id)">
                {{ initials(displayNameOf(row.msg.sender_id)) }}
              </div>
              <div class="msg-main">
                <!-- 群聊中展示他人昵称，否则分不清每条消息是谁发的；颜色与头像一致 -->
                <div
                  v-if="isGroup && row.msg.sender_id !== auth.user?.id"
                  class="sender"
                  :style="{ color: colorOf(row.msg.sender_id) }"
                >
                  {{ displayNameOf(row.msg.sender_id) }}
                </div>
                <div class="bubble" :class="{ recalled: row.msg.status === 2 }">
                  <template v-if="row.msg.status === 2">
                    <em>消息已撤回</em>
                  </template>
                  <template v-else-if="row.msg.type === 1">
                    {{ row.msg.content?.text }}
                  </template>
                  <template v-else-if="row.msg.type === 2">
                    <img :src="row.msg.content?.url" :alt="'image'" />
                  </template>
                  <template v-else-if="row.msg.type === 4">
                    <em>{{ row.msg.content?.text }}</em>
                  </template>
                  <template v-else>
                    <a :href="row.msg.content?.url" target="_blank">{{ row.msg.content?.name || '文件' }}</a>
                  </template>
                </div>
                <div class="msg-meta">
                  <span v-if="row.msg._status === 'sending'" class="sending">发送中…</span>
                  <!-- 发送失败可点击重发（沿用原幂等键，服务端去重） -->
                  <span
                    v-else-if="row.msg._status === 'failed'"
                    class="failed"
                    title="点击重新发送"
                    @click="resend(row.msg)"
                  >
                    发送失败 · 点击重发
                  </span>
                  <span>{{ fmtTime(row.msg.created_at) }}</span>
                </div>
              </div>
            </div>
          </template>
          </div>

          <!-- 用户未贴底时收到新消息：底部浮出胶囊，点击一键回到底部 -->
          <transition name="pill">
            <button v-if="newBelow > 0" class="new-msg-pill" @click="jumpToLatest">
              ↓ {{ newBelow > 1 ? `${newBelow} 条新消息` : '新消息' }}
            </button>
          </transition>
        </div>

        <footer class="composer">
          <input
            v-model="draft"
            placeholder="输入消息，Enter 发送"
            @input="onDraftInput"
            @keydown.enter.prevent="send"
          />
          <button class="send-btn" :disabled="!draft.trim()" @click="send">发送</button>
        </footer>
      </template>

      <!-- 空会话占位页 -->
      <div v-else class="placeholder">
        <div class="placeholder-icon" aria-hidden="true">
          <svg viewBox="0 0 24 24" width="56" height="56" fill="none">
            <path
              d="M21 11.5a8.5 8.5 0 0 1-8.5 8.5H4.6l-2.3 2.3a1 1 0 0 1-1.7-.7V11.5A8.5 8.5 0 0 1 9.1 3h3.4A8.5 8.5 0 0 1 21 11.5Z"
              stroke="currentColor" stroke-width="1.4" stroke-linejoin="round"
            />
            <circle cx="8.5" cy="11.5" r="1.2" fill="currentColor" />
            <circle cx="12.5" cy="11.5" r="1.2" fill="currentColor" />
            <circle cx="16.5" cy="11.5" r="1.2" fill="currentColor" />
          </svg>
        </div>
        <p class="placeholder-title">选择一个会话开始聊天</p>
        <p class="placeholder-sub">在中间列表选择会话，或到「单聊 / 群聊」分类下发起新会话</p>
      </div>
    </main>
  </div>
</template>

<script setup lang="ts">
/**
 * 聊天主页面（三栏布局：图标导航栏 + 分类列表栏 + 聊天主区）。
 *
 * - 第 1 栏（.rail）：分类导航（消息/单聊/群聊/联系人），附各类未读合计徽标、
 *   主题切换与退出入口；顶部头像叠连接状态点。
 * - 第 2 栏（.panel）：随分类切换标题与列表内容；顶部搜索框做前端过滤；
 *   发起单聊/群聊的输入区挪到对应分类面板顶部；联系人列表由单聊会话的 peer 派生。
 * - 第 3 栏（.main）：消息区与输入框，功能不变。
 *
 * 本组件只负责视图与交互编排，实际的消息收发、历史分页、已读上报、
 * WebSocket 重连等逻辑都封装在 chat store 中；分类/搜索状态是组件内局部状态，
 * 不写入、也不清空 chat store 的任何数据。
 */
import { computed, nextTick, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { useChatStore, type LocalMessage } from '@/stores/chat'
import { useThemeStore } from '@/stores/theme'
import ThemeToggle from '@/components/ThemeToggle.vue'
import { api, type User } from '@/api'

const auth = useAuthStore()
const chat = useChatStore()
const theme = useThemeStore()
const router = useRouter()

const draft = ref('')
const peerIdInput = ref('')
const groupNameInput = ref('')
const groupMembersInput = ref('')
const scrollEl = ref<HTMLElement | null>(null)

/** 分类导航状态（组件内局部状态，与 chat store 解耦） */
type Category = 'all' | 'direct' | 'group' | 'contacts'
const category = ref<Category>('all')
/** 第 2 栏搜索词：前端过滤当前分类列表 */
const searchText = ref('')

/** 历史翻页加载态与"已到顶"标记（按会话记忆，避免重复空拉） */
const historyLoading = ref(false)
const historyDoneSet = ref(new Set<number>())
/** 用户是否停留在消息区底部附近：决定新消息到达时是否自动跟随滚动 */
const nearBottom = ref(true)
/** 用户未贴底期间到达的新消息数：驱动底部"↓ 新消息"浮出胶囊 */
const newBelow = ref(0)
/** 翻历史时置位，抑制"消息数变化 → 滚到底部"的 watcher，改为恢复滚动锚点 */
let suppressAutoScroll = false
/** 上次发送 typing 帧的时间戳，用于 3 秒节流 */
let lastTypingAt = 0

/** 当前是否为群聊会话（type 2），决定消息区是否显示发送者昵称 */
const isGroup = computed(() => chat.activeConversation?.type === 2)

// 群聊发送者昵称缓存：避免每条消息都重复请求同一个用户的资料
const userCache = ref(new Map<number, User>())

/**
 * 头像/昵称配色盘：按用户 ID 哈希取色，保证同一用户全局同色。
 * 深色主题使用提亮版配色，保证在暗底上的可读性；色相选择饱和度适中的彩色，
 * 既能当文字色也能以低透明度做柔和底色。
 */
const PALETTE_LIGHT = ['#5b6cf0', '#0e9f8a', '#d97706', '#dc2626', '#7c3aed', '#db2777', '#0284c7', '#65a30d']
const PALETTE_DARK = ['#8b93f8', '#2dd4bf', '#fbbf24', '#f87171', '#a78bfa', '#f472b6', '#38bdf8', '#a3e635']
const palette = computed(() => (theme.mode === 'dark' ? PALETTE_DARK : PALETTE_LIGHT))

function colorOf(seed: number | string): string {
  const s = String(seed)
  let h = 0
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) >>> 0
  return palette.value[h % palette.value.length]
}

/** 头像样式：柔和底色（低透明度的同色系）+ 彩色文字，深色下底色略浓一点 */
function avatarStyle(seed: number | string) {
  const c = colorOf(seed)
  return { background: `${c}${theme.mode === 'dark' ? '33' : '26'}`, color: c }
}

/** 取昵称首字符作为头像占位（无头像图时的文字头像） */
function initials(name?: string) {
  if (!name) return '?'
  return name.slice(0, 1).toUpperCase()
}

/**
 * 任意用户的展示名：自己 → 当前昵称；单聊对方 → 会话 peer 信息（无需请求）；
 * 群聊成员 → 走昵称缓存（未命中时异步拉取）。
 */
function displayNameOf(userId: number): string {
  if (userId === auth.user?.id) return auth.user?.nickname || '我'
  const peer = chat.activeConversation?.peer
  if (peer && peer.id === userId) return peer.nickname || peer.username || `用户 ${userId}`
  return senderName(userId)
}

/** 会话列表项是否有对端正在输入 */
function convTyping(convId: number): boolean {
  return Object.keys(chat.typingUsers[convId] ?? {}).length > 0
}

/** 当前会话正在输入的用户名列表（驱动聊天头部的 typing 提示） */
const typingNames = computed<string[]>(() => {
  const bucket = chat.typingUsers[chat.activeConversationId] ?? {}
  return Object.keys(bucket).map((id) => displayNameOf(Number(id)))
})

const typingText = computed(() => {
  const names = typingNames.value
  if (names.length === 0) return ''
  if (names.length === 1) return `${names[0]} 正在输入`
  return `${names.length} 人正在输入`
})

/* ===== 三栏布局：分类导航 / 过滤 / 联系人派生 ===== */

/** 第 2 栏标题随分类切换 */
const panelTitle = computed(() => {
  const map: Record<Category, string> = { all: '消息', direct: '单聊', group: '群聊', contacts: '联系人' }
  return map[category.value]
})

/** 各类目未读合计（导航图标上的徽标） */
const unreadTotal = computed(() => chat.conversations.reduce((s, c) => s + c.member.unread_count, 0))
const unreadDirect = computed(() => chat.conversations.reduce((s, c) => (c.type === 1 ? s + c.member.unread_count : s), 0))
const unreadGroup = computed(() => chat.conversations.reduce((s, c) => (c.type === 2 ? s + c.member.unread_count : s), 0))

const norm = (s?: string) => (s ?? '').toLowerCase()

/** 当前分类 + 搜索词过滤后的会话列表 */
const filteredConversations = computed(() => {
  const kw = norm(searchText.value)
  return chat.conversations.filter((c) => {
    if (category.value === 'direct' && c.type !== 1) return false
    if (category.value === 'group' && c.type !== 2) return false
    if (!kw) return true
    const name = c.peer?.nickname || c.peer?.username || c.title || `#${c.id}`
    return norm(name).includes(kw)
  })
})

/**
 * 联系人列表：从单聊会话（type=1）中提取对端用户，按 id 去重。
 * 纯前端派生，不额外请求接口。
 */
const contacts = computed<User[]>(() => {
  const map = new Map<number, User>()
  for (const c of chat.conversations) {
    if (c.type === 1 && c.peer && c.peer.id !== auth.user?.id) map.set(c.peer.id, c.peer)
  }
  return [...map.values()]
})

/** 联系人 + 搜索词过滤 */
const filteredContacts = computed(() => {
  const kw = norm(searchText.value)
  if (!kw) return contacts.value
  return contacts.value.filter((u) => norm(u.nickname).includes(kw) || norm(u.username).includes(kw))
})

/** 会话列表空态文案随分类/搜索态变化 */
const convEmptyText = computed(() => {
  if (searchText.value) return '没有匹配的会话'
  const map: Record<Category, string> = {
    all: '暂无会话，发起一个吧',
    direct: '暂无单聊会话',
    group: '暂无群聊会话',
    contacts: '',
  }
  return map[category.value]
})

/** 切换分类：同时清空搜索词，避免旧关键词误过滤新分类 */
function selectCategory(c: Category) {
  category.value = c
  searchText.value = ''
}

/** 联系人是否在线（presence 帧维护的在线集合） */
function isOnline(userId: number): boolean {
  return chat.onlineUsers.has(userId)
}

/** 时间显示：24 小时内显示时刻，更早的显示月-日 */
function fmtTime(iso?: string) {
  if (!iso) return ''
  const d = new Date(iso)
  const now = Date.now()
  if (now - d.getTime() < 86_400_000) {
    return d.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
  }
  return d.toLocaleDateString('zh-CN', { month: '2-digit', day: '2-digit' })
}

/** 时间分隔条文案：今天只显示时刻，今年显示 月-日+时刻，跨年带年份 */
function fmtDivider(ts: number) {
  const d = new Date(ts)
  const now = new Date()
  const hm = d.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
  if (d.toDateString() === now.toDateString()) return hm
  const sameYear = d.getFullYear() === now.getFullYear()
  const md = d.toLocaleDateString(
    'zh-CN',
    sameYear ? { month: 'numeric', day: 'numeric' } : { year: 'numeric', month: 'numeric', day: 'numeric' },
  )
  return `${md} ${hm}`
}

interface MsgRow {
  msg: LocalMessage
  /** 非空表示该消息前需要插入时间分隔条 */
  divider: string | null
}

/** 消息列表预处理：首条及与上一条间隔 >10 分钟的消息前插入时间分隔条 */
const msgRows = computed<MsgRow[]>(() => {
  const rows: MsgRow[] = []
  let prev = 0
  for (const m of chat.activeMessages) {
    const t = Date.parse(m.created_at)
    const valid = !Number.isNaN(t)
    rows.push({
      msg: m,
      divider: valid && (prev === 0 || t - prev > 10 * 60_000) ? fmtDivider(t) : null,
    })
    if (valid) prev = t
  }
  return rows
})

/** 会话列表的最后一条消息预览文案，按消息类型折叠为占位符 */
function preview(m?: LocalMessage) {
  if (!m) return ''
  if (m.status === 2) return '[已撤回]'
  switch (m.type) {
    case 1: return m.content?.text ?? ''
    case 2: return '[图片]'
    case 3: return '[文件]'
    case 4: return m.content?.text ?? '[系统]'
    default: return '[消息]'
  }
}

/** 输入框内容变化：3 秒节流发送 typing 帧，通知对端"正在输入" */
function onDraftInput() {
  const now = Date.now()
  if (now - lastTypingAt < 3000 || !chat.activeConversationId) return
  lastTypingAt = now
  chat.sendTyping(chat.activeConversationId)
}

/** 发送当前输入的文本消息；store 内部做乐观上屏与 ack 状态回写 */
async function send() {
  const text = draft.value.trim()
  if (!text || !chat.activeConversationId) return
  draft.value = ''
  nearBottom.value = true
  newBelow.value = 0
  await chat.sendText(chat.activeConversationId, text)
  await nextTick()
  scrollToBottom()
}

/** 点击失败消息重发：沿用原 client_msg_id，服务端幂等去重 */
function resend(m: LocalMessage) {
  if (m._status !== 'failed') return
  void chat.resendMessage(m)
}

function scrollToBottom() {
  if (scrollEl.value) scrollEl.value.scrollTop = scrollEl.value.scrollHeight
}

/** 滚动监听：维护"是否贴近底部"标记；接近顶部时向上翻页拉历史 */
function onScroll() {
  const el = scrollEl.value
  if (!el) return
  nearBottom.value = el.scrollHeight - el.scrollTop - el.clientHeight < 80
  // 用户滚回底部，"新消息"胶囊随之消失
  if (nearBottom.value) newBelow.value = 0
  if (el.scrollTop <= 20) void loadOlder()
}

/** 点击"↓ 新消息"胶囊：滚到底部并清零计数 */
function jumpToLatest() {
  nearBottom.value = true
  newBelow.value = 0
  scrollToBottom()
}

/**
 * 向上翻页拉取更早的历史消息。
 * 拉取前记录滚动位置，插入旧消息后按滚动高度差恢复锚点，
 * 避免视口跳动（同时抑制自动滚到底部的 watcher）。
 */
async function loadOlder() {
  const convId = chat.activeConversationId
  const el = scrollEl.value
  if (!convId || historyLoading.value || historyDoneSet.value.has(convId)) return

  historyLoading.value = true
  suppressAutoScroll = true
  const beforeH = el?.scrollHeight ?? 0
  const beforeTop = el?.scrollTop ?? 0
  const beforeCount = chat.activeMessages.length
  try {
    await chat.pullHistory(convId)
  } catch {
    // 拉取失败不标记到顶，下次滚到顶部可重试
  }
  historyLoading.value = false

  const added = chat.activeMessages.length - beforeCount
  if (added <= 0) {
    // 换一个新 Set 触发响应式，驱动"已经到顶啦"提示
    historyDoneSet.value = new Set(historyDoneSet.value).add(convId)
  }
  await nextTick()
  if (el && added > 0) el.scrollTop = el.scrollHeight - beforeH + beforeTop
  suppressAutoScroll = false
}

/**
 * 按对方用户 ID 打开单聊：后端幂等复用已有会话，刷新列表后直接打开。
 * 供"发起单聊"输入框与联系人列表点击共用。
 */
async function openDirectWith(userId: number) {
  try {
    const conv = await api.createDirect(userId)
    await chat.loadConversations()
    await chat.openConversation(conv.id)
  } catch (e) {
    console.error(e)
  }
}

/** 发起单聊：解析输入框里的用户 ID 后走 openDirectWith */
async function startDirect() {
  const id = Number(peerIdInput.value)
  if (!id || Number.isNaN(id)) return
  peerIdInput.value = ''
  await openDirectWith(id)
}

/** 点击联系人：幂等创建/复用单聊会话并直接打开 */
async function openContact(userId: number) {
  await openDirectWith(userId)
}

/** 发起群聊：填群名和逗号分隔的成员 ID（自己作为群主自动入群） */
async function startGroup() {
  const name = groupNameInput.value
  if (!name) return
  const memberIds = groupMembersInput.value
    .split(/[,，\s]+/)
    .map((s) => Number(s))
    .filter((n) => Number.isInteger(n) && n > 0 && n !== auth.user?.id)
  try {
    const conv = await api.createGroup(name, memberIds)
    groupNameInput.value = ''
    groupMembersInput.value = ''
    await chat.loadConversations()
    await chat.openConversation(conv.id)
  } catch (e) {
    console.error(e)
  }
}

/**
 * 取群聊消息发送者的昵称用于展示。
 * 命中缓存直接返回；未命中先返回占位文案并异步拉取资料写入缓存
 * （写入 ref 包着的 Map 会触发重新渲染，昵称随后自动替换占位符）。
 */
function senderName(senderId: number): string {
  const cached = userCache.value.get(senderId)
  if (cached) return cached.nickname || cached.username || `用户 ${senderId}`
  void api
    .getUser(senderId)
    .then((u) => userCache.value.set(senderId, u))
    .catch(() => userCache.value.set(senderId, { id: senderId, nickname: `用户 ${senderId}` } as User))
  return `用户 ${senderId}`
}

/** 退出登录：通知后端吊销令牌、拆除 WS 连接与本地聊天状态后回登录页 */
async function logout() {
  await auth.logout()
  chat.teardown()
  router.push({ name: 'login' })
}

// 切换会话后等 DOM 更新再滚到底部，保证定位到最新消息
watch(
  () => chat.activeConversationId,
  async () => {
    nearBottom.value = true
    newBelow.value = 0
    await nextTick()
    scrollToBottom()
  },
)

// 新消息到达后滚到底部——但仅当用户本来就贴在底部（或消息是自己发的），
// 避免用户向上翻历史时被新消息拽回底部；未贴底时改为累计"新消息"胶囊计数
watch(
  () => chat.activeMessages.length,
  async () => {
    if (suppressAutoScroll) return
    const list = chat.activeMessages
    const last = list[list.length - 1]
    const mine = !!last && last.sender_id === auth.user?.id
    if (!nearBottom.value && !mine) {
      newBelow.value += 1
      return
    }
    await nextTick()
    scrollToBottom()
  },
)
</script>

<style scoped>
/* ===== 整体布局：光斑层铺底（.bg-fx 全局类），三栏内容压在其上 ===== */
.chat-layout {
  position: relative;
  display: flex;
  height: 100%;
  background: var(--bg-page);
  overflow: hidden; /* 裁掉光斑漂移的越界部分 */
}
.rail, .panel, .main { position: relative; z-index: 1; }

/* ===== 第 1 栏：图标导航栏（深色窄栏，Discord 风格） ===== */
.rail {
  width: 68px;
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  padding: var(--space-3) 0;
  gap: var(--space-3);
  background: var(--bg-rail);
  border-right: 1px solid var(--border);
  transition: background-color var(--dur-base) ease, border-color var(--dur-base) ease;
}

/* 顶部我的头像 + 连接状态点；悬停轻微放大（头像不是按钮，仅给柔和反馈） */
.rail-me {
  position: relative;
  margin-bottom: var(--space-1);
  transition: transform 0.25s cubic-bezier(0.34, 1.25, 0.64, 1);
}
.rail-me:hover { transform: scale(1.04); }
.status-dot {
  position: absolute;
  right: -2px; bottom: -2px;
  width: 11px; height: 11px;
  border-radius: 50%;
  background: var(--color-warning);
  border: 2px solid var(--bg-rail); /* 描边色与栏底一致，形成镂空效果 */
}
@media (prefers-reduced-motion: no-preference) {
  .status-dot:not(.on) { animation: pulse 1.2s ease-in-out infinite; } /* 重连中呼吸闪烁 */
}
.status-dot.on { background: var(--color-success); }
@keyframes pulse { 50% { opacity: 0.35; } }

.rail-nav { display: flex; flex-direction: column; gap: 6px; flex: 1; }
.rail-bottom { display: flex; flex-direction: column; align-items: center; gap: 6px; }

/* 导航图标按钮：默认灰，hover 提亮，选中态主色填充 + 栏左缘指示条 */
.rail-btn {
  position: relative;
  width: 44px; height: 44px;
  display: flex; align-items: center; justify-content: center;
  border: none;
  background: transparent;
  border-radius: var(--radius-md);
  color: var(--text-3);
  padding: 0;
  /* 颜色类属性走 0.22s 缓出；transform 单独用柔和弹性曲线（轻微过冲回弹，无抖动） */
  transition: background-color 0.22s var(--ease-out), color 0.22s var(--ease-out), box-shadow 0.22s var(--ease-out), filter 0.22s var(--ease-out), transform 0.25s cubic-bezier(0.34, 1.25, 0.64, 1);
}
/* 悬停（未选中项）：低饱和主色薄雾底 + 文字柔提亮 + 轻微放大与淡影，柔和不刺眼 */
.rail-btn:not(.on):hover:not(:disabled) {
  background: color-mix(in srgb, var(--color-primary) 9%, transparent);
  color: var(--text-1);
  transform: scale(1.05);
  box-shadow: var(--shadow-bubble);
}
/* 已选中项悬停仅极轻微提亮，保留渐变选中态 */
.rail-btn.on:hover { filter: brightness(1.05); }
/* 按压轻微回缩，松手随弹性曲线回弹 */
.rail-btn:active:not(:disabled) { transform: scale(0.97); }
.rail-btn.on {
  background: linear-gradient(135deg, var(--color-primary), var(--color-primary-hover));
  color: var(--color-on-primary);
  box-shadow: var(--shadow-primary);
}
/* 选中态左侧指示条：贴在导航栏左缘（按钮居中，用负 left 探到栏边缘） */
.rail-btn.on::before {
  content: '';
  position: absolute;
  left: -12px; top: 50%;
  transform: translateY(-50%);
  width: 4px; height: 55%;
  border-radius: 0 var(--radius-full) var(--radius-full) 0;
  background: var(--color-primary);
}

/* 导航图标上的未读合计徽标：右上角小红胶囊，描边镂空 */
.rail-badge {
  position: absolute;
  top: -3px; right: -5px;
  min-width: 16px; height: 16px;
  display: inline-flex; align-items: center; justify-content: center;
  background: var(--badge-bg);
  color: var(--color-on-primary);
  font-size: 10px; font-weight: 600;
  border-radius: var(--radius-full);
  padding: 0 4px;
  border: 2px solid var(--bg-rail);
}

/* 导航栏里的主题切换按钮：改为透明底图标风格，与其他栏内按钮一致 */
.rail-bottom :deep(.theme-toggle) {
  width: 44px; height: 44px;
  border: none;
  background: transparent;
  border-radius: var(--radius-md);
  color: var(--text-3);
  /* 与 rail-btn 同一套柔和节奏：颜色 0.22s 缓出，缩放 0.25s 轻微回弹 */
  transition: background-color 0.22s var(--ease-out), color 0.22s var(--ease-out), transform 0.25s cubic-bezier(0.34, 1.25, 0.64, 1);
}
.rail-bottom :deep(.theme-toggle:hover:not(:disabled)) {
  background: color-mix(in srgb, var(--color-primary) 9%, transparent);
  color: var(--text-1);
  transform: scale(1.05);
}

/* ===== 第 2 栏：分类列表栏（玻璃拟态） ===== */
.panel {
  width: 280px;
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  border-right: 1px solid var(--border);
  background: var(--bg-glass);
  backdrop-filter: blur(14px) saturate(1.2);
  -webkit-backdrop-filter: blur(14px) saturate(1.2);
  transition: background-color var(--dur-base) ease, border-color var(--dur-base) ease;
}

.panel-header {
  padding: var(--space-3) var(--space-3) var(--space-2);
  border-bottom: 1px solid var(--border);
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}
.panel-title { font-size: var(--text-lg); font-weight: 700; padding: 0 var(--space-1); }

/* 搜索框：放大镜图标内嵌左侧 */
.search-box { position: relative; }
.search-box svg {
  position: absolute;
  left: 10px; top: 50%;
  transform: translateY(-50%);
  color: var(--text-3);
  pointer-events: none;
}
.search-box input {
  width: 100%;
  padding: 7px 10px 7px 30px;
  font-size: var(--text-sm);
  border-radius: var(--radius-md);
}

/* 断线横幅：窄栏里只有一个状态点，文字说明放在列表栏顶部 */
.conn-banner {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: var(--space-2) var(--space-3) 0;
  padding: 6px 10px;
  border-radius: var(--radius-sm);
  background: var(--color-warning-soft);
  color: var(--color-warning);
  font-size: var(--text-xs);
  font-weight: 500;
}
.conn-banner .dot {
  width: 7px; height: 7px;
  border-radius: 50%;
  background: var(--color-warning);
  flex-shrink: 0;
}
@media (prefers-reduced-motion: no-preference) {
  .conn-banner .dot { animation: pulse 1.2s ease-in-out infinite; }
}

/* ---- 分类快捷操作（发起单聊/群聊） ---- */
.panel-actions {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-3);
  border-bottom: 1px solid var(--border);
}
.new-chat, .new-group .row2 { display: flex; gap: 6px; }
.new-chat input, .new-group input { flex: 1; min-width: 0; padding: 7px 10px; font-size: var(--text-xs); }
.new-group { display: flex; flex-direction: column; gap: 6px; }
.ghost-btn { font-size: var(--text-xs); padding: 6px 10px; flex-shrink: 0; color: var(--color-primary); border-color: var(--color-primary-border); }
.ghost-btn:hover:not(:disabled) { background: var(--color-primary-soft); }

/* ---- 会话/联系人列表 ---- */
.conv-list { list-style: none; margin: 0; padding: var(--space-2); overflow-y: auto; flex: 1; }
.conv-list li {
  position: relative;
  display: flex;
  gap: 10px;
  padding: 10px 12px;
  border-radius: var(--radius-md);
  cursor: pointer;
  align-items: center;
  transition: background var(--dur-fast) var(--ease-out);
}
.conv-list li:hover { background: var(--bg-hover); }
/* 选中态：主色浅底 + 左侧主色指示条 */
.conv-list li.active { background: var(--color-primary-soft); }
.conv-list li.active::before {
  content: '';
  position: absolute;
  left: 0; top: 50%;
  transform: translateY(-50%);
  width: 3px; height: 60%;
  border-radius: var(--radius-full);
  background: var(--color-primary);
}
.conv-list li.active strong { color: var(--color-primary); }
.conv-list li.empty { justify-content: center; color: var(--text-3); font-size: var(--text-sm); cursor: default; padding: var(--space-5) var(--space-3); text-align: center; }
.conv-list li.empty:hover { background: transparent; }

.avatar {
  width: 38px; height: 38px;
  border-radius: var(--radius-md); /* 方形圆角头像比纯圆更现代 */
  display: flex; align-items: center; justify-content: center;
  font-weight: 600; font-size: var(--text-base);
  flex-shrink: 0;
  user-select: none;
}
/* 自己的头像用主色渐变实心，在导航栏里突出身份 */
.me-avatar {
  width: 40px; height: 40px;
  color: var(--color-on-primary);
  background: linear-gradient(135deg, var(--color-primary), var(--color-primary-grad-end));
}

.body { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 2px; }
.row { display: flex; justify-content: space-between; align-items: center; gap: var(--space-2); }
.row strong { font-size: var(--text-base); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.row small { color: var(--text-3); font-size: var(--text-xs); flex-shrink: 0; }
.preview { font-size: var(--text-sm); color: var(--text-2); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.typing-preview { font-size: var(--text-sm); color: var(--color-primary); font-style: italic; }

/* 联系人在线状态：灰点离线 / 绿点在线 */
.contact-status {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: var(--text-xs);
  color: var(--text-3);
}
.contact-status .dot { width: 6px; height: 6px; border-radius: 50%; background: var(--text-3); }
.contact-status.on { color: var(--color-success); }
.contact-status.on .dot { background: var(--color-success); }

/* 未读角标：胶囊形、渐变红、超 99 折叠 */
.badge {
  min-width: 18px; height: 18px;
  display: inline-flex; align-items: center; justify-content: center;
  background: var(--badge-bg);
  color: var(--color-on-primary);
  font-size: 11px; font-weight: 600;
  border-radius: var(--radius-full);
  padding: 0 5px;
  flex-shrink: 0;
  box-shadow: var(--shadow-badge);
}

/* ===== 第 3 栏：聊天主区（透明底，透出光斑层） ===== */
.main { flex: 1; display: flex; flex-direction: column; min-width: 0; }

.chat-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-5);
  border-bottom: 1px solid var(--border);
  /* 玻璃拟态顶栏 */
  background: var(--bg-glass-strong);
  backdrop-filter: blur(14px) saturate(1.2);
  -webkit-backdrop-filter: blur(14px) saturate(1.2);
  box-shadow: var(--shadow-header);
  z-index: 1; /* 让阴影压在消息区之上 */
  transition: background-color var(--dur-base) ease, border-color var(--dur-base) ease;
}
.title-wrap { display: flex; align-items: baseline; gap: 10px; min-width: 0; }
.title-wrap strong { font-size: var(--text-lg); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.conv-id { color: var(--text-3); font-size: var(--text-xs); flex-shrink: 0; }

/* 头部 typing 提示：主色文字 + 三个波浪式依次跳动的圆点 */
.typing-hint {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  font-size: var(--text-sm);
  color: var(--color-primary);
  flex-shrink: 0;
}
.typing-hint .tdot {
  width: 4px; height: 4px;
  border-radius: 50%;
  background: var(--color-primary);
}
@media (prefers-reduced-motion: no-preference) {
  .typing-hint .tdot { animation: wave 1.2s ease-in-out infinite; }
  .typing-hint .tdot:nth-child(2) { animation-delay: 0.12s; }
  .typing-hint .tdot:nth-child(3) { animation-delay: 0.24s; }
}
@keyframes wave {
  0%, 60%, 100% { transform: translateY(0) scale(1); opacity: 0.5; }
  30% { transform: translateY(-4px) scale(1.2); opacity: 1; }
}
@media (prefers-reduced-motion: no-preference) {
  .fade-enter-active, .fade-leave-active { transition: opacity var(--dur-base) var(--ease-out); }
}
.fade-enter-from, .fade-leave-to { opacity: 0; }

/* ---- 消息区包装：承载滚动列表与"新消息"浮层胶囊 ---- */
.messages-wrap {
  position: relative;
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
.messages {
  flex: 1;
  overflow-y: auto;
  padding: var(--space-5) var(--space-5) var(--space-4);
  /* 半透明渐变底（令牌为渐变值），底层光斑隐约透出 */
  background: var(--bg-messages);
}

.history-tip {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  color: var(--text-3);
  font-size: var(--text-xs);
  padding-bottom: var(--space-3);
}
.spinner {
  width: 13px; height: 13px;
  border: 2px solid var(--border-strong);
  border-top-color: var(--color-primary);
  border-radius: 50%;
  animation: spin 0.7s linear infinite; /* 加载指示属功能性反馈，常转 */
}
@keyframes spin { to { transform: rotate(360deg); } }

/* 时间分隔条：居中胶囊 */
.time-divider { display: flex; justify-content: center; margin: 2px 0 var(--space-4); }
.time-divider span {
  font-size: var(--text-xs);
  color: var(--text-3);
  background: var(--bg-hover);
  padding: 2px 12px;
  border-radius: var(--radius-full);
}

/* ---- 消息行：头像 + 气泡列；自己的整体右对齐 ----
 * content-visibility + contain-intrinsic-size：长列表中屏外消息行跳过渲染，
 * 显著降低大会话的渲染成本（浏览器原生虚拟化能力，无需 JS 库）。
 */
.msg {
  display: flex;
  gap: 10px;
  margin-bottom: var(--space-4);
  content-visibility: auto;
  contain-intrinsic-size: auto 64px;
}
/* 新消息弹性入场：淡入 + 轻缩小回弹 + 上移（尊重减少动效偏好） */
@media (prefers-reduced-motion: no-preference) {
  .msg { animation: msg-in 0.3s var(--ease-spring); }
}
@keyframes msg-in {
  from { opacity: 0; transform: translateY(10px) scale(0.96); }
}
.msg.mine { flex-direction: row-reverse; }
.msg .avatar { width: 34px; height: 34px; font-size: var(--text-sm); margin-top: 2px; }

.msg-main { display: flex; flex-direction: column; align-items: flex-start; max-width: 68%; min-width: 0; }
.msg.mine .msg-main { align-items: flex-end; }

.sender { font-size: var(--text-xs); font-weight: 500; margin: 0 0 3px 4px; }

/* ---- 气泡：非对称圆角（对方左下直角 / 自己右下直角），指向头像一侧 ---- */
.bubble {
  padding: 9px 13px;
  border-radius: 12px 12px 12px 4px;
  background: var(--bg-card);
  border: 1px solid var(--border);
  box-shadow: var(--shadow-bubble);
  font-size: var(--text-base);
  line-height: 1.55;
  word-break: break-word;
  white-space: pre-wrap;
  transition: background-color var(--dur-base) ease, border-color var(--dur-base) ease, color var(--dur-base) ease;
}
.msg.mine .bubble {
  border-radius: 12px 12px 4px 12px;
  background: linear-gradient(135deg, var(--color-primary), var(--color-primary-hover));
  border-color: transparent;
  color: var(--color-on-primary);
  box-shadow: var(--shadow-primary);
  position: relative;
  overflow: hidden; /* 把微光扫过裁进气泡圆角内 */
}
/* 自己气泡的渐变微光：一道周期性扫过的高光带（动画只改 transform） */
.msg.mine .bubble::after {
  content: '';
  position: absolute;
  inset: 0;
  pointer-events: none;
  background: linear-gradient(105deg, transparent 42%, rgba(255, 255, 255, 0.16) 50%, transparent 58%);
  transform: translateX(-130%);
}
@media (prefers-reduced-motion: no-preference) {
  .msg.mine .bubble::after { animation: sheen 6s ease-in-out 1.2s infinite; }
}
@keyframes sheen {
  0% { transform: translateX(-130%); }
  55%, 100% { transform: translateX(130%); }
}
/* 撤回的消息退化为灰色中性样式（并去掉微光） */
.bubble.recalled {
  background: var(--bg-hover);
  border-color: var(--border);
  color: var(--text-3);
  font-style: italic;
  box-shadow: none;
}
.msg.mine .bubble.recalled { background: var(--bg-hover); color: var(--text-3); }
.msg.mine .bubble.recalled::after { display: none; }
.bubble img { max-width: 240px; border-radius: var(--radius-sm); display: block; }
.msg.mine .bubble a { color: var(--color-on-primary); text-decoration: underline; }

.msg-meta { font-size: var(--text-xs); color: var(--text-3); margin-top: 4px; display: flex; gap: var(--space-2); padding: 0 2px; }
.sending { color: var(--text-3); }
.failed { color: var(--color-danger); cursor: pointer; font-weight: 500; }
.failed:hover { text-decoration: underline; }

/* ---- 输入区（玻璃拟态） ---- */
.composer {
  display: flex;
  gap: 10px;
  padding: var(--space-3) var(--space-5);
  border-top: 1px solid var(--border);
  background: var(--bg-glass-strong);
  backdrop-filter: blur(14px) saturate(1.2);
  -webkit-backdrop-filter: blur(14px) saturate(1.2);
  transition: background-color var(--dur-base) ease, border-color var(--dur-base) ease;
}
.composer input { flex: 1; padding: 10px 14px; border-radius: var(--radius-md); font-size: var(--text-base); }
.send-btn {
  border: none;
  color: var(--color-on-primary);
  font-weight: 600;
  padding: 0 22px;
  border-radius: var(--radius-md);
  background: linear-gradient(135deg, var(--color-primary), var(--color-primary-hover));
  box-shadow: var(--shadow-primary);
  transition: filter var(--dur-fast) var(--ease-out), box-shadow var(--dur-fast) var(--ease-out), transform var(--dur-fast) var(--ease-out);
}
.send-btn:hover:not(:disabled) { background: linear-gradient(135deg, var(--color-primary), var(--color-primary-hover)); filter: brightness(1.06); }
.send-btn:active:not(:disabled) { transform: scale(0.95); }
.send-btn:disabled { box-shadow: none; }
/* 输入框有内容（发送键可用）时轻微脉冲呼吸，引导发送 */
@media (prefers-reduced-motion: no-preference) {
  .send-btn:not(:disabled) { animation: btn-pulse 2.4s ease-in-out infinite; }
}
@keyframes btn-pulse {
  0%, 100% { transform: scale(1); }
  50% { transform: scale(1.04); }
}

/* ---- "↓ 新消息"浮出胶囊 ---- */
.new-msg-pill {
  position: absolute;
  bottom: 14px;
  left: 50%;
  translate: -50% 0; /* 用独立 translate 属性居中，不与过渡的 transform 冲突 */
  border: none;
  color: var(--color-on-primary);
  font-size: var(--text-sm);
  font-weight: 500;
  padding: 7px 16px;
  border-radius: var(--radius-full);
  background: linear-gradient(135deg, var(--color-primary), var(--color-primary-hover));
  box-shadow: var(--shadow-pop);
  z-index: 2;
}
.new-msg-pill:hover:not(:disabled) { background: linear-gradient(135deg, var(--color-primary), var(--color-primary-hover)); filter: brightness(1.06); }
@media (prefers-reduced-motion: no-preference) {
  .pill-enter-active, .pill-leave-active {
    transition: opacity var(--dur-base) var(--ease-spring), transform var(--dur-base) var(--ease-spring);
  }
}
.pill-enter-from, .pill-leave-to { opacity: 0; transform: translateY(10px) scale(0.9); }

/* ---- 空会话占位页（透明底透出光斑） ---- */
.placeholder {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
}
.placeholder-icon {
  width: 96px; height: 96px;
  display: flex; align-items: center; justify-content: center;
  border-radius: 28px;
  color: var(--color-primary);
  background: var(--color-primary-soft);
  margin-bottom: var(--space-3);
}
.placeholder-title { margin: 0; font-size: var(--text-lg); font-weight: 600; color: var(--text-1); }
.placeholder-sub { margin: 0; font-size: var(--text-sm); color: var(--text-3); }

/* ---- 窄屏兜底：第 2 栏收窄，主区弹性 ---- */
@media (max-width: 900px) {
  .panel { width: 220px; }
}
</style>
