<template>
  <!-- 群管理抽屉：从聊天主区右侧滑出，展示成员列表并支持拉人/踢人 -->
  <aside class="drawer">
    <header class="drawer-header">
      <strong>成员（{{ members.length }}）</strong>
      <button class="drawer-close" title="关闭" @click="emit('close')">×</button>
    </header>

    <!-- 成员列表：头像 / 昵称 / 角色角标 / 在线状态 / 踢出按钮（群主与管理员可见） -->
    <ul class="member-list">
      <li v-if="loading" class="tip"><span class="spinner"></span>加载中…</li>
      <li v-else-if="members.length === 0" class="tip">暂无成员</li>
      <li v-for="m in members" :key="m.user_id" class="member-item">
        <div class="avatar" :style="avatarStyle(m.user_id)">{{ initials(m.nickname || m.username) }}</div>
        <div class="member-body">
          <div class="member-name">
            <strong>{{ m.nickname || m.username || `用户 ${m.user_id}` }}</strong>
            <!-- 角色角标：群主金色 / 管理员主色 -->
            <span v-if="m.role === 2" class="role-tag owner">群主</span>
            <span v-else-if="m.role === 1" class="role-tag admin">管理员</span>
          </div>
          <span :class="['online-tag', { on: chat.onlineUsers.has(m.user_id) }]">
            <i class="dot"></i>{{ chat.onlineUsers.has(m.user_id) ? '在线' : '离线' }}
          </span>
        </div>
        <button
          v-if="canKick(m)"
          class="kick-btn"
          title="踢出该成员"
          :disabled="actingId === m.user_id"
          @click="kick(m)"
        >
          踢出
        </button>
      </li>
    </ul>

    <!-- 邀请成员：关键词搜索全网用户，点结果即拉入 -->
    <footer class="invite-box">
      <div class="invite-title">邀请成员</div>
      <input v-model.trim="kw" placeholder="搜索用户（昵称 / 用户名）" />
      <ul v-if="kw" class="invite-results">
        <li v-if="searching" class="tip"><span class="spinner"></span>搜索中…</li>
        <li v-else-if="inviteCandidates.length === 0" class="tip">无匹配用户</li>
        <li v-for="u in inviteCandidates" :key="u.id" class="invite-item" @click="invite(u)">
          <div class="avatar sm" :style="avatarStyle(u.id)">{{ initials(u.nickname || u.username) }}</div>
          <div class="member-body">
            <strong>{{ u.nickname || u.username || `用户 ${u.id}` }}</strong>
            <small v-if="u.username" class="username">@{{ u.username }}</small>
          </div>
          <span class="plus">＋</span>
        </li>
      </ul>
    </footer>
  </aside>
</template>

<script setup lang="ts">
/**
 * 群管理抽屉。
 *
 * - 成员列表来自 GET /conversations/:id/members，切换会话时自动重载；
 * - 踢人权限：群主可踢管理员与普通成员，管理员只能踢普通成员（服务端兜底校验）；
 * - 邀请：关键词防抖 300ms 调 /users/search，点结果拉入后刷新成员列表；
 *   候选结果自动排除已在群成员（inviteCandidates 计算属性）。
 */
import { computed, ref, watch } from 'vue'
import { api, type MemberInfo, type User } from '@/api'
import { useAuthStore } from '@/stores/auth'
import { useChatStore } from '@/stores/chat'
import { useAvatarColor } from '@/utils/avatar'

const props = defineProps<{ convId: number; myRole: number }>()
const emit = defineEmits<{ close: [] }>()

const auth = useAuthStore()
const chat = useChatStore()
const { avatarStyle, initials } = useAvatarColor()

const members = ref<MemberInfo[]>([])
const loading = ref(false)
/** 正在执行踢人/拉人操作的成员 id：防重复点击 */
const actingId = ref(0)

const kw = ref('')
const searchResults = ref<User[]>([])
const searching = ref(false)
let searchTimer: number | undefined

/** 邀请候选 = 搜索结果中尚未入群的用户 */
const inviteCandidates = computed(() => {
  const inGroup = new Set(members.value.map((m) => m.user_id))
  return searchResults.value.filter((u) => !inGroup.has(u.id) && u.id !== auth.user?.id)
})

/** 拉取成员列表：群主/管理员排前面，其余按入群时间 */
async function load() {
  loading.value = true
  try {
    const list = await api.listMembers(props.convId)
    members.value = [...list].sort((a, b) => b.role - a.role)
  } catch (e) {
    console.error('[drawer] load members failed', e)
  } finally {
    loading.value = false
  }
}
watch(() => props.convId, load, { immediate: true })

/** 邀请搜索：防抖 300ms */
watch(kw, (v) => {
  window.clearTimeout(searchTimer)
  const q = v.trim()
  if (!q) {
    searchResults.value = []
    return
  }
  searchTimer = window.setTimeout(async () => {
    searching.value = true
    try {
      searchResults.value = await api.searchUsers(q)
    } catch {
      searchResults.value = []
    } finally {
      searching.value = false
    }
  }, 300)
})

/** 踢人权限：群主可踢非群主；管理员只能踢普通成员；不能踢自己 */
function canKick(m: MemberInfo): boolean {
  if (m.user_id === auth.user?.id || m.role === 2) return false
  if (props.myRole === 2) return true
  return props.myRole === 1 && m.role === 0
}

async function kick(m: MemberInfo) {
  actingId.value = m.user_id
  try {
    await api.removeMember(props.convId, m.user_id)
    await load()
  } catch (e) {
    console.error('[drawer] kick failed', e)
  } finally {
    actingId.value = 0
  }
}

async function invite(u: User) {
  actingId.value = u.id
  try {
    await api.addMember(props.convId, u.id)
    await load()
  } catch (e) {
    console.error('[drawer] invite failed', e)
  } finally {
    actingId.value = 0
  }
}
</script>

<style scoped>
.drawer {
  position: absolute;
  top: 0; right: 0; bottom: 0;
  width: 300px;
  display: flex;
  flex-direction: column;
  background: var(--bg-glass-strong);
  backdrop-filter: blur(16px) saturate(1.2);
  -webkit-backdrop-filter: blur(16px) saturate(1.2);
  border-left: 1px solid var(--border);
  box-shadow: var(--shadow-pop);
  z-index: 3;
}

.drawer-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-4);
  border-bottom: 1px solid var(--border);
}
.drawer-header strong { font-size: var(--text-base); }
.drawer-close {
  border: none;
  background: transparent;
  font-size: 20px;
  line-height: 1;
  padding: 2px 8px;
  color: var(--text-3);
}
.drawer-close:hover:not(:disabled) { color: var(--text-1); background: var(--bg-hover); }

.member-list { list-style: none; margin: 0; padding: var(--space-2); flex: 1; overflow-y: auto; }
.member-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 10px;
  border-radius: var(--radius-md);
  transition: background var(--dur-fast) var(--ease-out);
}
.member-item:hover { background: var(--bg-hover); }

.avatar {
  width: 34px; height: 34px;
  border-radius: var(--radius-md);
  display: flex; align-items: center; justify-content: center;
  font-weight: 600; font-size: var(--text-sm);
  flex-shrink: 0;
  user-select: none;
}
.avatar.sm { width: 30px; height: 30px; font-size: var(--text-xs); }

.member-body { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 1px; }
.member-name { display: flex; align-items: center; gap: 6px; }
.member-name strong {
  font-size: var(--text-sm);
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.role-tag {
  font-size: 10px;
  padding: 0 5px;
  border-radius: var(--radius-full);
  flex-shrink: 0;
  font-weight: 600;
}
.role-tag.owner { color: var(--color-warning); background: var(--color-warning-soft); }
.role-tag.admin { color: var(--color-primary); background: var(--color-primary-soft); }

.online-tag { display: inline-flex; align-items: center; gap: 4px; font-size: var(--text-xs); color: var(--text-3); }
.online-tag .dot { width: 6px; height: 6px; border-radius: 50%; background: var(--text-3); }
.online-tag.on { color: var(--color-success); }
.online-tag.on .dot { background: var(--color-success); }

.kick-btn {
  font-size: var(--text-xs);
  padding: 3px 8px;
  color: var(--color-danger);
  border-color: var(--color-danger-border);
  flex-shrink: 0;
}
.kick-btn:hover:not(:disabled) { background: var(--color-danger-soft); }

.tip {
  display: flex; align-items: center; justify-content: center; gap: var(--space-2);
  color: var(--text-3); font-size: var(--text-xs);
  padding: var(--space-4) 0;
  list-style: none;
}
.spinner {
  width: 13px; height: 13px;
  border: 2px solid var(--border-strong);
  border-top-color: var(--color-primary);
  border-radius: 50%;
  animation: spin 0.7s linear infinite;
}
@keyframes spin { to { transform: rotate(360deg); } }

.invite-box {
  border-top: 1px solid var(--border);
  padding: var(--space-3);
}
.invite-title { font-size: var(--text-sm); font-weight: 600; margin-bottom: var(--space-2); }
.invite-box input { width: 100%; font-size: var(--text-sm); }
.invite-results {
  list-style: none;
  margin: var(--space-2) 0 0;
  padding: 0;
  max-height: 180px;
  overflow-y: auto;
}
.invite-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 7px 8px;
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: background var(--dur-fast) var(--ease-out);
}
.invite-item:hover { background: var(--bg-hover); }
.invite-item strong { font-size: var(--text-sm); }
.username { color: var(--text-3); font-size: var(--text-xs); }
.plus { color: var(--color-primary); font-weight: 600; flex-shrink: 0; }
</style>
