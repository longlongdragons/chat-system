<template>
  <!-- 发起群聊弹窗：群名称 + 搜索多选成员 + 已选 chips；遮罩点击/ESC（父组件统一处理）关闭 -->
  <div class="gcm-mask" @click.self="close">
    <div class="gcm-card" role="dialog" aria-label="发起群聊">
      <header class="gcm-header">
        <strong>发起群聊</strong>
        <button class="gcm-close" data-tip="关闭" aria-label="关闭" @click="close">×</button>
      </header>

      <!-- 群名称（必填） -->
      <input v-model.trim="groupName" class="name-input" placeholder="给群聊起个名字" maxlength="32" />

      <!-- 成员搜索：防抖 300ms 调 /users/search -->
      <input v-model.trim="kw" class="search-input" placeholder="搜索用户（昵称/用户名/ID）" />

      <!-- 已选成员 chips：横排可单个移除 -->
      <div v-if="selected.length" class="chips">
        <span v-for="u in selected" :key="u.id" class="chip">
          <span class="chip-avatar" :style="avatarStyle(u.id)">{{ initials(u.nickname || u.username) }}</span>
          <span class="chip-name">{{ u.nickname || u.username || `用户 ${u.id}` }}</span>
          <button class="chip-x" :aria-label="`移除 ${u.nickname || u.username}`" @click="toggle(u)">×</button>
        </span>
      </div>

      <!-- 候选列表：checkbox 多选，已选打勾；候选中排除自己 -->
      <ul class="candidates">
        <li v-if="searching" class="tip">搜索中…</li>
        <li v-else-if="!kw" class="tip">输入关键词搜索要邀请的成员（也可直接创建空群）</li>
        <li v-else-if="candidates.length === 0" class="tip">无匹配用户</li>
        <li v-for="u in candidates" :key="u.id" class="cand" @click="toggle(u)">
          <span :class="['checkbox', { on: isSelected(u.id) }]">
            <svg v-if="isSelected(u.id)" viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round">
              <path d="m4.5 12.5 5 5 10-11" />
            </svg>
          </span>
          <span class="avatar" :style="avatarStyle(u.id)">{{ initials(u.nickname || u.username) }}</span>
          <div class="cand-body">
            <strong>{{ u.nickname || u.username || `用户 ${u.id}` }}</strong>
            <small v-if="u.username" class="username">@{{ u.username }}</small>
          </div>
        </li>
      </ul>

      <p v-if="error" class="error" role="alert">{{ error }}</p>

      <footer class="gcm-footer">
        <!-- 允许 0 个成员直接创建：自己先建群，之后用群管理抽屉拉人 -->
        <span class="hint">{{ selected.length ? `已选 ${selected.length} 人` : '可先创建，之后再加人' }}</span>
        <button class="create-btn" :disabled="!groupName.trim() || creating" @click="create">
          <span v-if="creating" class="spinner"></span>{{ creating ? '创建中…' : '创建群聊' }}
        </button>
      </footer>
    </div>
  </div>
</template>

<script setup lang="ts">
/**
 * 发起群聊弹窗。
 *
 * 交互流程：填群名（必填）→ 搜索用户（防抖 300ms）→ 点候选行 checkbox 多选
 * （已选以 chips 横排展示、可单个移除）→「创建群聊」。
 * 允许 0 个成员直接创建（自己作为群主先入群，之后通过群管理抽屉拉人）。
 * 成功后 emit('created', 会话)，由父组件负责刷新会话列表并打开新群。
 */
import { computed, onUnmounted, ref, watch } from 'vue'
import { api, type Conversation, type User } from '@/api'
import { useAuthStore } from '@/stores/auth'
import { useAvatarColor } from '@/utils/avatar'

const emit = defineEmits<{
  close: []
  created: [conv: Conversation]
}>()

const auth = useAuthStore()
const { avatarStyle, initials } = useAvatarColor()

const groupName = ref('')
const kw = ref('')
const results = ref<User[]>([])
const searching = ref(false)
const selected = ref<User[]>([])
const creating = ref(false)
const error = ref('')
let searchTimer: number | undefined

/** 候选列表 = 搜索结果排除自己（已选中的保留并打勾，不重复加进 chips） */
const candidates = computed(() => results.value.filter((u) => u.id !== auth.user?.id))

/** 成员搜索：防抖 300ms；清空关键词时立即清空结果 */
watch(kw, (v) => {
  window.clearTimeout(searchTimer)
  const q = v.trim()
  if (!q) {
    results.value = []
    return
  }
  searchTimer = window.setTimeout(async () => {
    searching.value = true
    try {
      results.value = await api.searchUsers(q)
    } catch {
      results.value = []
    } finally {
      searching.value = false
    }
  }, 300)
})

onUnmounted(() => window.clearTimeout(searchTimer))

function isSelected(id: number): boolean {
  return selected.value.some((u) => u.id === id)
}

/** 点候选行：未选则加入 chips，已选则移除 */
function toggle(u: User) {
  if (isSelected(u.id)) selected.value = selected.value.filter((x) => x.id !== u.id)
  else selected.value = [...selected.value, u]
}

function close() {
  if (!creating.value) emit('close')
}

/** 创建群聊：名称必填，成员可为空；成功后把新会话交给父组件 */
async function create() {
  const name = groupName.value.trim()
  if (!name || creating.value) return
  creating.value = true
  error.value = ''
  try {
    const conv = await api.createGroup(name, selected.value.map((u) => u.id))
    emit('created', conv)
  } catch (e: any) {
    error.value = e?.response?.data?.message || e?.message || '创建失败'
  } finally {
    creating.value = false
  }
}
</script>

<style scoped>
.gcm-mask {
  position: fixed;
  inset: 0;
  z-index: 25;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(15, 18, 28, 0.35);
  backdrop-filter: blur(4px);
  -webkit-backdrop-filter: blur(4px);
}
.gcm-card {
  width: 400px;
  max-width: calc(100vw - 32px);
  max-height: 80vh;
  display: flex;
  flex-direction: column;
  background: var(--bg-glass-card);
  backdrop-filter: blur(18px) saturate(1.3);
  -webkit-backdrop-filter: blur(18px) saturate(1.3);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-card);
  padding: var(--space-4) var(--space-5);
}
@media (prefers-reduced-motion: no-preference) {
  .gcm-card { animation: card-in 0.25s var(--ease-spring); }
}
@keyframes card-in {
  from { opacity: 0; transform: translateY(12px) scale(0.96); }
}

.gcm-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: var(--space-3);
}
.gcm-header strong { font-size: var(--text-lg); }
.gcm-close {
  border: none;
  background: transparent;
  font-size: 20px;
  line-height: 1;
  padding: 2px 8px;
  color: var(--text-3);
}
.gcm-close:hover:not(:disabled) { color: var(--text-1); background: var(--bg-hover); }

.name-input, .search-input { width: 100%; margin-bottom: var(--space-3); padding: 9px 12px; }

/* 已选成员 chips：横排换行，头像 + 昵称 + × */
.chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-bottom: var(--space-3);
  max-height: 96px;
  overflow-y: auto;
}
.chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 3px 6px 3px 3px;
  border-radius: var(--radius-full);
  background: var(--color-primary-soft);
  font-size: var(--text-xs);
  color: var(--text-1);
}
.chip-avatar {
  width: 20px; height: 20px;
  border-radius: 50%;
  display: inline-flex; align-items: center; justify-content: center;
  font-size: 10px; font-weight: 600;
}
.chip-name { max-width: 96px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.chip-x {
  border: none;
  background: transparent;
  color: var(--text-3);
  font-size: 13px;
  line-height: 1;
  padding: 0 3px;
}
.chip-x:hover:not(:disabled) { color: var(--color-danger); background: transparent; }

.candidates {
  list-style: none;
  margin: 0;
  padding: 2px;
  flex: 1;
  min-height: 120px;
  overflow-y: auto;
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
}
.cand {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 10px;
  border-radius: var(--radius-sm);
  cursor: pointer;
  transition: background var(--dur-fast) var(--ease-out);
}
.cand:hover { background: var(--bg-hover); }

/* 自绘 checkbox：选中态主色填充 + 白色对勾 */
.checkbox {
  width: 18px; height: 18px;
  flex-shrink: 0;
  border: 1.5px solid var(--border-strong);
  border-radius: 5px;
  display: inline-flex; align-items: center; justify-content: center;
  color: var(--color-on-primary);
  transition: background var(--dur-fast) var(--ease-out), border-color var(--dur-fast) var(--ease-out), transform var(--dur-fast) var(--ease-out);
}
.checkbox.on {
  background: var(--color-primary);
  border-color: var(--color-primary);
}

.avatar {
  width: 32px; height: 32px;
  border-radius: var(--radius-md);
  display: inline-flex; align-items: center; justify-content: center;
  font-weight: 600; font-size: var(--text-sm);
  flex-shrink: 0;
  user-select: none;
}
.cand-body { flex: 1; min-width: 0; display: flex; flex-direction: column; }
.cand-body strong { font-size: var(--text-sm); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.username { color: var(--text-3); font-size: var(--text-xs); }

.tip {
  display: flex; align-items: center; justify-content: center;
  color: var(--text-3); font-size: var(--text-xs);
  padding: var(--space-5) var(--space-3);
  text-align: center;
  list-style: none;
}

.error {
  color: var(--color-danger);
  background: var(--color-danger-soft);
  border: 1px solid var(--color-danger-border);
  border-radius: var(--radius-sm);
  font-size: var(--text-sm);
  margin: var(--space-3) 0 0;
  padding: 8px 10px;
}

.gcm-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  margin-top: var(--space-3);
}
.hint { font-size: var(--text-xs); color: var(--text-3); }
.create-btn {
  border: none;
  color: var(--color-on-primary);
  font-weight: 600;
  padding: 8px 20px;
  border-radius: var(--radius-md);
  background: linear-gradient(135deg, var(--color-primary), var(--color-primary-hover));
  box-shadow: var(--shadow-primary);
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  flex-shrink: 0;
}
.create-btn:hover:not(:disabled) { filter: brightness(1.06); }
.create-btn:disabled { box-shadow: none; }
.spinner {
  width: 12px; height: 12px;
  border: 2px solid rgba(255, 255, 255, 0.4);
  border-top-color: #fff;
  border-radius: 50%;
  animation: spin 0.7s linear infinite;
}
@keyframes spin { to { transform: rotate(360deg); } }
</style>
