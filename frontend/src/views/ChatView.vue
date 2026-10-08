<template>
  <div class="chat-layout">
    <!-- 环境光斑背景层：三个大模糊渐变球缓慢漂移（纯装饰，pointer-events 关闭） -->
    <div class="bg-fx" aria-hidden="true"><i class="blob b1"></i><i class="blob b2"></i><i class="blob b3"></i></div>

    <!-- 侧边栏 -->
    <aside class="sidebar">
      <header>
        <div class="me">
          <div class="avatar me-avatar">{{ initials(auth.user?.nickname) }}</div>
          <div class="meta">
            <strong>{{ auth.user?.nickname }}</strong>
            <!-- 连接状态徽标：已连接绿点 / 重连中黄点 -->
            <span :class="['conn-badge', { on: chat.connected }]">
              <i class="dot"></i>{{ chat.connected ? '已连接' : '重连中…' }}
            </span>
          </div>
        </div>
        <div class="header-actions">
          <ThemeToggle />
          <button class="logout" title="退出登录" @click="logout">退出</button>
        </div>
      </header>

      <div class="quick-actions">
        <div class="new-chat">
          <input v-model.trim="peerIdInput" placeholder="对方用户 ID" @keyup.enter="startDirect" />
          <button class="ghost-btn" @click="startDirect">单聊</button>
        </div>
        <div class="new-group">
          <input v-model.trim="groupNameInput" placeholder="群名称" @keyup.enter="startGroup" />
          <div class="row2">
            <input
              v-model.trim="groupMembersInput"
              placeholder="成员 ID，逗号分隔，如 1,2"
              @keyup.enter="startGroup"
            />
            <button class="ghost-btn" @click="startGroup">群聊</button>
          </div>
        </div>
      </div>

      <ul class="conv-list">
        <li
          v-for="c in chat.conversations"
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
        <li v-if="chat.conversations.length === 0" class="empty">暂无会话，发起一个吧</li>
      </ul>
    </aside>

    <!-- 聊天区 -->
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
        <p class="placeholder-sub">从左侧列表选择会话，或输入用户 ID 发起新的单聊 / 群聊</p>
      </div>
    </main>
  </div>
</template>

<script setup lang="ts">
/**
 * 聊天主页面。
 *
 * 左侧为会话列表（含未读角标、最后一条消息预览、连接状态指示），
 * 右侧为当前会话的消息区与输入框。
 *
 * 本组件只负责视图与交互编排，实际的消息收发、历史分页、已读上报、
 * WebSocket 重连等逻辑都封装在 chat store 中：
 * - 发送消息走 store 的乐观更新（先上屏，等网关 ack 关联回写状态）；
 * - 向上滚动到顶部时按序号游标分页拉取更早的历史消息。
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

/** 按对方用户 ID 发起单聊：后端幂等复用已有会话，随后刷新列表并直接打开 */
async function startDirect() {
  const id = Number(peerIdInput.value)
  if (!id || Number.isNaN(id)) return
  try {
    const conv = await api.createDirect(id)
    peerIdInput.value = ''
    await chat.loadConversations()
    await chat.openConversation(conv.id)
  } catch (e) {
    console.error(e)
  }
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
/* ===== 整体布局：光斑层铺底（.bg-fx 全局类），侧栏与主区内容压在其上 ===== */
.chat-layout {
  position: relative;
  display: flex;
  height: 100%;
  background: var(--bg-page);
  overflow: hidden; /* 裁掉光斑漂移的越界部分 */
}
.sidebar, .main { position: relative; z-index: 1; }

.sidebar {
  width: 300px;
  flex-shrink: 0;
  border-right: 1px solid var(--border);
  display: flex;
  flex-direction: column;
  /* 玻璃拟态：半透明底 + 背景模糊，底层光斑隐约透出 */
  background: var(--bg-glass);
  backdrop-filter: blur(14px) saturate(1.2);
  -webkit-backdrop-filter: blur(14px) saturate(1.2);
  transition: background-color var(--dur-base) ease, border-color var(--dur-base) ease;
}

/* ---- 侧栏头部：我的信息 + 连接状态 + 主题切换 ---- */
.sidebar > header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-3) var(--space-4);
  border-bottom: 1px solid var(--border);
}
.header-actions { display: flex; align-items: center; gap: 6px; flex-shrink: 0; }
.me { display: flex; align-items: center; gap: 10px; min-width: 0; }
.meta { display: flex; flex-direction: column; line-height: 1.3; min-width: 0; }
.meta strong { font-size: var(--text-base); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

.conn-badge {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: var(--text-xs);
  color: var(--color-warning);
}
.conn-badge .dot {
  width: 7px; height: 7px;
  border-radius: 50%;
  background: var(--color-warning);
}
/* 重连中黄点呼吸闪烁，提示连接不稳定 */
@media (prefers-reduced-motion: no-preference) {
  .conn-badge .dot { animation: pulse 1.2s ease-in-out infinite; }
}
.conn-badge.on { color: var(--color-success); }
.conn-badge.on .dot { background: var(--color-success); animation: none; }
@keyframes pulse { 50% { opacity: 0.35; } }

.logout { font-size: var(--text-xs); padding: 4px 10px; color: var(--text-2); }

/* ---- 发起单聊/群聊快捷区 ---- */
.quick-actions {
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

/* ---- 会话列表 ---- */
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
.conv-list li.empty { justify-content: center; color: var(--text-3); font-size: var(--text-sm); cursor: default; }
.conv-list li.empty:hover { background: transparent; }

.avatar {
  width: 38px; height: 38px;
  border-radius: var(--radius-md); /* 方形圆角头像比纯圆更现代 */
  display: flex; align-items: center; justify-content: center;
  font-weight: 600; font-size: var(--text-base);
  flex-shrink: 0;
  user-select: none;
}
/* 自己的头像用主色渐变实心，在列表中突出身份 */
.me-avatar {
  width: 36px; height: 36px;
  color: var(--color-on-primary);
  background: linear-gradient(135deg, var(--color-primary), var(--color-primary-grad-end));
}

.body { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 2px; }
.row { display: flex; justify-content: space-between; align-items: center; gap: var(--space-2); }
.row strong { font-size: var(--text-base); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.row small { color: var(--text-3); font-size: var(--text-xs); flex-shrink: 0; }
.preview { font-size: var(--text-sm); color: var(--text-2); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.typing-preview { font-size: var(--text-sm); color: var(--color-primary); font-style: italic; }

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

/* ===== 主聊天区（透明底，透出光斑层） ===== */
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
</style>
