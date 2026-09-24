<template>
  <div class="chat-layout">
    <!-- 侧边栏 -->
    <aside class="sidebar">
      <header>
        <div class="me">
          <div class="avatar">{{ initials(auth.user?.nickname) }}</div>
          <div class="meta">
            <strong>{{ auth.user?.nickname }}</strong>
            <small :class="{ online: chat.connected }">
              {{ chat.connected ? '已连接' : '重连中…' }}
            </small>
          </div>
        </div>
        <button class="logout" @click="logout">退出</button>
      </header>

      <div class="new-chat">
        <input v-model.trim="peerIdInput" placeholder="对方用户 ID" @keyup.enter="startDirect" />
        <button @click="startDirect">发起单聊</button>
      </div>

      <ul class="conv-list">
        <li
          v-for="c in chat.conversations"
          :key="c.id"
          :class="{ active: c.id === chat.activeConversationId }"
          @click="chat.openConversation(c.id)"
        >
          <div class="avatar">
            {{ initials(c.peer?.nickname || c.title || `#${c.id}`) }}
          </div>
          <div class="body">
            <div class="row">
              <strong>{{ c.peer?.nickname || c.title || `会话 ${c.id}` }}</strong>
              <small>{{ fmtTime(c.last_msg_at) }}</small>
            </div>
            <div class="row">
              <span class="preview">{{ preview(c.last_msg) }}</span>
              <span v-if="c.member.unread_count > 0" class="badge">{{ c.member.unread_count }}</span>
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
          <strong>
            {{ chat.activeConversation?.peer?.nickname || chat.activeConversation?.title || '会话' }}
          </strong>
          <small>#{{ chat.activeConversationId }}</small>
        </header>

        <div ref="scrollEl" class="messages" @scroll="onScroll">
          <div
            v-for="m in chat.activeMessages"
            :key="m.id || m.client_msg_id"
            :class="['msg', { mine: m.sender_id === auth.user?.id }]"
          >
            <div class="bubble" :class="{ recalled: m.status === 2 }">
              <template v-if="m.status === 2">
                <em>消息已撤回</em>
              </template>
              <template v-else-if="m.type === 1">
                {{ m.content?.text }}
              </template>
              <template v-else-if="m.type === 2">
                <img :src="m.content?.url" :alt="'image'" />
              </template>
              <template v-else-if="m.type === 4">
                <em>{{ m.content?.text }}</em>
              </template>
              <template v-else>
                <a :href="m.content?.url" target="_blank">{{ m.content?.name || '文件' }}</a>
              </template>
            </div>
            <div class="meta">
              <span v-if="m._status === 'sending'">发送中…</span>
              <span v-else-if="m._status === 'failed'" class="failed">发送失败</span>
              <span>{{ fmtTime(m.created_at) }}</span>
            </div>
          </div>
        </div>

        <footer class="composer">
          <input
            v-model="draft"
            placeholder="输入消息，Enter 发送"
            @keydown.enter.prevent="send"
          />
          <button :disabled="!draft.trim()" @click="send">发送</button>
        </footer>
      </template>

      <div v-else class="placeholder">选择一个会话开始聊天</div>
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
import { nextTick, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { useChatStore, type LocalMessage } from '@/stores/chat'
import { api } from '@/api'

const auth = useAuthStore()
const chat = useChatStore()
const router = useRouter()

const draft = ref('')
const peerIdInput = ref('')
const scrollEl = ref<HTMLElement | null>(null)

/** 取昵称首字符作为头像占位（无头像图时的文字头像） */
function initials(name?: string) {
  if (!name) return '?'
  return name.slice(0, 1).toUpperCase()
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

/** 发送当前输入的文本消息；store 内部做乐观上屏与 ack 状态回写 */
async function send() {
  const text = draft.value.trim()
  if (!text || !chat.activeConversationId) return
  draft.value = ''
  await chat.sendText(chat.activeConversationId, text)
  await nextTick()
  scrollToBottom()
}

function scrollToBottom() {
  if (scrollEl.value) scrollEl.value.scrollTop = scrollEl.value.scrollHeight
}

/** 滚动到接近顶部时向上翻页，拉取更早的历史消息（按 seq 游标分页） */
function onScroll() {
  if (!scrollEl.value) return
  if (scrollEl.value.scrollTop <= 20) {
    void chat.pullHistory(chat.activeConversationId)
  }
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
    await nextTick()
    scrollToBottom()
  },
)

// 新消息到达（含自己发送的乐观消息）后同样滚到底部
watch(
  () => chat.activeMessages.length,
  async () => {
    await nextTick()
    scrollToBottom()
  },
)
</script>

<style scoped>
.chat-layout { display: flex; height: 100%; background: #fff; }
.sidebar {
  width: 300px;
  border-right: 1px solid #e6e8eb;
  display: flex;
  flex-direction: column;
  background: #fafbfc;
}
.sidebar header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 14px 16px;
  border-bottom: 1px solid #e6e8eb;
}
.me { display: flex; align-items: center; gap: 10px; }
.meta { display: flex; flex-direction: column; line-height: 1.3; }
.meta small { color: #6e7781; font-size: 12px; }
.meta small.online { color: #1a7f37; }
.avatar {
  width: 34px; height: 34px; border-radius: 50%;
  background: #0969da; color: #fff;
  display: flex; align-items: center; justify-content: center;
  font-weight: 600; flex-shrink: 0;
}
.new-chat {
  display: flex; gap: 6px; padding: 10px 12px;
  border-bottom: 1px solid #e6e8eb;
}
.new-chat input { flex: 1; min-width: 0; }
.conv-list { list-style: none; margin: 0; padding: 6px; overflow-y: auto; flex: 1; }
.conv-list li {
  display: flex; gap: 10px; padding: 10px;
  border-radius: 8px; cursor: pointer; align-items: center;
}
.conv-list li:hover { background: #eef2f6; }
.conv-list li.active { background: #dbeafe; }
.conv-list li.empty { justify-content: center; color: #8b949e; font-size: 13px; cursor: default; }
.body { flex: 1; min-width: 0; }
.row { display: flex; justify-content: space-between; align-items: center; gap: 8px; }
.row strong { font-size: 14px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.row small { color: #8b949e; font-size: 11px; flex-shrink: 0; }
.preview { font-size: 12px; color: #6e7781; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.badge {
  background: #cf222e; color: #fff; font-size: 11px;
  border-radius: 10px; padding: 1px 6px; flex-shrink: 0;
}
.main { flex: 1; display: flex; flex-direction: column; min-width: 0; }
.chat-header {
  padding: 14px 20px; border-bottom: 1px solid #e6e8eb;
  display: flex; align-items: baseline; gap: 10px;
}
.chat-header small { color: #8b949e; }
.messages { flex: 1; overflow-y: auto; padding: 16px 20px; background: #f6f8fa; }
.msg { display: flex; flex-direction: column; margin-bottom: 14px; align-items: flex-start; }
.msg.mine { align-items: flex-end; }
.bubble {
  max-width: 62%; padding: 8px 12px; border-radius: 10px;
  background: #fff; border: 1px solid #e6e8eb;
  word-break: break-word; white-space: pre-wrap;
}
.msg.mine .bubble { background: #0969da; color: #fff; border-color: #0969da; }
.bubble.recalled { opacity: 0.6; font-style: italic; }
.bubble img { max-width: 240px; border-radius: 6px; display: block; }
.meta { font-size: 11px; color: #8b949e; margin-top: 4px; display: flex; gap: 8px; }
.meta .failed { color: #cf222e; }
.composer {
  display: flex; gap: 8px; padding: 12px 16px;
  border-top: 1px solid #e6e8eb;
}
.composer input { flex: 1; }
.placeholder {
  flex: 1; display: flex; align-items: center; justify-content: center;
  color: #8b949e;
}
</style>