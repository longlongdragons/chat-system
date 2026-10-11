<template>
  <!-- 个人资料卡：点击导航栏头像弹出，可编辑昵称与签名 -->
  <div class="profile-mask" @click.self="emit('close')">
    <div class="profile-card" role="dialog" aria-label="个人资料">
      <header class="profile-header">
        <div class="avatar lg" :style="avatarStyle(auth.user?.id ?? 0)">
          {{ initials(auth.user?.nickname || auth.user?.username) }}
        </div>
        <div class="who">
          <strong>{{ auth.user?.nickname || auth.user?.username }}</strong>
          <small v-if="auth.user?.username">@{{ auth.user.username }}</small>
        </div>
        <button class="close" title="关闭" @click="emit('close')">×</button>
      </header>

      <label class="field">
        <span class="field-label">昵称</span>
        <input v-model.trim="nickname" maxlength="32" placeholder="给自己起个好名字" />
      </label>
      <label class="field">
        <span class="field-label">签名</span>
        <textarea v-model.trim="signature" rows="3" maxlength="120" placeholder="一句话介绍自己"></textarea>
      </label>

      <p v-if="error" class="error" role="alert">{{ error }}</p>

      <footer class="profile-footer">
        <button @click="emit('close')">取消</button>
        <button class="save-btn" :disabled="saving || !dirty" @click="save">
          <span v-if="saving" class="spinner"></span>{{ saving ? '保存中…' : '保存' }}
        </button>
      </footer>
    </div>
  </div>
</template>

<script setup lang="ts">
/**
 * 个人资料编辑卡（模态）。
 * 保存调 PATCH /users/me（auth store 的 updateProfile），
 * 成功后本地 user 整体替换为服务端返回值，导航栏头像等处自动更新。
 */
import { computed, ref } from 'vue'
import { useAuthStore } from '@/stores/auth'
import { useAvatarColor } from '@/utils/avatar'

const emit = defineEmits<{ close: [] }>()

const auth = useAuthStore()
const { avatarStyle, initials } = useAvatarColor()

const nickname = ref(auth.user?.nickname ?? '')
const signature = ref(auth.user?.signature ?? '')
const saving = ref(false)
const error = ref('')

/** 有改动才允许保存，避免无谓请求 */
const dirty = computed(
  () => nickname.value !== (auth.user?.nickname ?? '') || signature.value !== (auth.user?.signature ?? ''),
)

async function save() {
  saving.value = true
  error.value = ''
  try {
    await auth.updateProfile({
      nickname: nickname.value || undefined,
      signature: signature.value,
    })
    emit('close')
  } catch (e: any) {
    error.value = e?.response?.data?.message || e?.message || '保存失败'
  } finally {
    saving.value = false
  }
}
</script>

<style scoped>
.profile-mask {
  position: fixed;
  inset: 0;
  z-index: 20;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(15, 18, 28, 0.35);
  backdrop-filter: blur(4px);
  -webkit-backdrop-filter: blur(4px);
}
.profile-card {
  width: 380px;
  max-width: calc(100vw - 32px);
  background: var(--bg-glass-card);
  backdrop-filter: blur(18px) saturate(1.3);
  -webkit-backdrop-filter: blur(18px) saturate(1.3);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-card);
  padding: var(--space-5);
}
@media (prefers-reduced-motion: no-preference) {
  .profile-card { animation: card-in 0.25s var(--ease-spring); }
}
@keyframes card-in {
  from { opacity: 0; transform: translateY(12px) scale(0.96); }
}

.profile-header { display: flex; align-items: center; gap: var(--space-3); margin-bottom: var(--space-4); }
.avatar.lg {
  width: 52px; height: 52px;
  border-radius: var(--radius-md);
  display: flex; align-items: center; justify-content: center;
  font-weight: 700; font-size: var(--text-xl);
  user-select: none;
}
.who { flex: 1; min-width: 0; display: flex; flex-direction: column; }
.who strong { font-size: var(--text-lg); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.who small { color: var(--text-3); font-size: var(--text-xs); }
.close {
  border: none; background: transparent;
  font-size: 20px; line-height: 1;
  padding: 2px 8px; color: var(--text-3);
  align-self: flex-start;
}
.close:hover:not(:disabled) { color: var(--text-1); background: var(--bg-hover); }

.field { display: block; margin-bottom: var(--space-4); }
.field-label { display: block; font-size: var(--text-sm); font-weight: 500; color: var(--text-2); margin-bottom: var(--space-2); }
.field input, .field textarea { width: 100%; resize: none; }

.error {
  color: var(--color-danger);
  background: var(--color-danger-soft);
  border: 1px solid var(--color-danger-border);
  border-radius: var(--radius-sm);
  font-size: var(--text-sm);
  margin: 0 0 var(--space-4);
  padding: 8px 10px;
}

.profile-footer { display: flex; justify-content: flex-end; gap: var(--space-2); }
.save-btn {
  border: none;
  color: var(--color-on-primary);
  font-weight: 600;
  padding: 7px 18px;
  border-radius: var(--radius-md);
  background: linear-gradient(135deg, var(--color-primary), var(--color-primary-hover));
  box-shadow: var(--shadow-primary);
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
}
.save-btn:hover:not(:disabled) { filter: brightness(1.06); }
.save-btn:disabled { box-shadow: none; }
.spinner {
  width: 12px; height: 12px;
  border: 2px solid rgba(255, 255, 255, 0.4);
  border-top-color: #fff;
  border-radius: 50%;
  animation: spin 0.7s linear infinite;
}
@keyframes spin { to { transform: rotate(360deg); } }
</style>
