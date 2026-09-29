<template>
  <div class="login-wrap">
    <div class="card">
      <!-- 品牌区：产品标识 + 副标题 -->
      <div class="brand">
        <div class="logo" aria-hidden="true">
          <svg viewBox="0 0 24 24" fill="none" width="26" height="26">
            <path
              d="M21 12a8 8 0 0 1-8 8H5l-1.7 1.7A1 1 0 0 1 1.6 21l-.1-.44V12a8 8 0 0 1 8-8H13a8 8 0 0 1 8 8Z"
              fill="currentColor" opacity=".92"
            />
            <circle cx="8.5" cy="12" r="1.3" fill="#fff" />
            <circle cx="13" cy="12" r="1.3" fill="#fff" />
            <circle cx="17.5" cy="12" r="1.3" fill="#fff" />
          </svg>
        </div>
        <h1>Chat System</h1>
        <p class="slogan">简单高效的即时沟通，随时随地保持连接</p>
      </div>

      <!-- 登录 / 注册 分段切换控件 -->
      <div class="segment" role="tablist">
        <button
          type="button"
          role="tab"
          :class="['segment-item', { on: mode === 'login' }]"
          :aria-selected="mode === 'login'"
          @click="switchMode('login')"
        >
          登录
        </button>
        <button
          type="button"
          role="tab"
          :class="['segment-item', { on: mode === 'register' }]"
          :aria-selected="mode === 'register'"
          @click="switchMode('register')"
        >
          注册
        </button>
      </div>

      <form @submit.prevent="submit" novalidate>
        <label class="field">
          <span class="field-label">账号</span>
          <input v-model.trim="form.login" placeholder="用户名 / 邮箱 / 手机号" required autofocus />
        </label>

        <!-- 注册模式才需要昵称；用过渡让高度变化不生硬 -->
        <transition name="slide">
          <label v-if="mode === 'register'" class="field">
            <span class="field-label">昵称</span>
            <input v-model.trim="form.nickname" placeholder="可选，默认使用账号名" />
          </label>
        </transition>

        <label class="field">
          <span class="field-label">密码</span>
          <input v-model="form.password" type="password" placeholder="至少 6 位" required minlength="6" />
        </label>

        <!-- 错误信息内联展示，带图标与抖动提示 -->
        <p v-if="error" class="error" role="alert">
          <svg viewBox="0 0 16 16" width="14" height="14" fill="currentColor" aria-hidden="true">
            <path d="M8 1a7 7 0 1 0 0 14A7 7 0 0 0 8 1Zm-.75 4a.75.75 0 0 1 1.5 0v3.5a.75.75 0 0 1-1.5 0V5ZM8 11.8a.9.9 0 1 1 0-1.8.9.9 0 0 1 0 1.8Z" />
          </svg>
          {{ error }}
        </p>

        <button type="submit" class="submit" :disabled="loading || !canSubmit">
          <span v-if="loading" class="spinner" aria-hidden="true"></span>
          {{ loading ? '处理中…' : mode === 'login' ? '登 录' : '注册并登录' }}
        </button>
      </form>

      <p class="tip">
        {{ mode === 'login' ? '还没有账号？' : '已经有账号了？' }}
        <a class="toggle" @click="switchMode(mode === 'login' ? 'register' : 'login')">
          {{ mode === 'login' ? '立即注册' : '直接登录' }}
        </a>
      </p>
    </div>
  </div>
</template>

<script setup lang="ts">
/**
 * 登录 / 注册页（同一表单双模式切换）。
 *
 * 登录成功后初始化聊天 store（拉会话列表 + 建立 WebSocket），
 * 并按路由守卫留下的 redirect 参数回跳原目标页（默认进聊天页）。
 */
import { computed, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { useChatStore } from '@/stores/chat'

const auth = useAuthStore()
const chat = useChatStore()
const router = useRouter()
const route = useRoute()

const mode = ref<'login' | 'register'>('login')
const loading = ref(false)
const error = ref('')

const form = reactive({ login: '', password: '', nickname: '' })

/** 提交按钮禁用条件：必填项为空时不允许提交（加载中也禁用，防重复点击） */
const canSubmit = computed(() => form.login.length > 0 && form.password.length >= 6)

function switchMode(m: 'login' | 'register') {
  mode.value = m
  error.value = ''
}

/** 提交登录/注册表单；成功后初始化聊天连接并回跳目标页 */
async function submit() {
  if (!canSubmit.value || loading.value) return
  error.value = ''
  loading.value = true
  try {
    if (mode.value === 'login') {
      // 登录账号支持用户名 / 邮箱 / 手机号，由后端统一识别
      await auth.login(form.login, form.password)
    } else {
      // 注册接口要求区分字段：按是否含 @ 判断填邮箱还是用户名
      const isEmail = form.login.includes('@')
      await auth.register({
        password: form.password,
        nickname: form.nickname || undefined,
        ...(isEmail ? { email: form.login } : { username: form.login }),
      })
    }
    // 注册/登录拿到令牌后立即建立聊天上下文，进入主页即可收发消息
    await chat.init()
    router.push((route.query.redirect as string) || { name: 'chat' })
  } catch (e: any) {
    // 优先展示后端信封里的业务错误信息
    error.value = e?.response?.data?.message || e?.message || '操作失败'
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
/* 页面底：主色极浅渐变，让白色卡片浮起来 */
.login-wrap {
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-4);
  background:
    radial-gradient(1200px 500px at 80% -10%, rgba(91, 108, 240, 0.10), transparent 60%),
    radial-gradient(900px 420px at 10% 110%, rgba(91, 108, 240, 0.08), transparent 60%),
    var(--bg-page);
}

.card {
  width: 400px;
  max-width: 100%;
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  padding: 36px 32px 28px;
  box-shadow: var(--shadow-card);
  animation: card-in 0.35s var(--ease-out);
}
@keyframes card-in {
  from { opacity: 0; transform: translateY(10px) scale(0.98); }
}

/* ---- 品牌区 ---- */
.brand { text-align: center; margin-bottom: var(--space-5); }
.logo {
  width: 52px; height: 52px;
  margin: 0 auto var(--space-3);
  border-radius: 14px;
  display: flex; align-items: center; justify-content: center;
  color: #fff;
  background: linear-gradient(135deg, var(--color-primary), #8b5cf6);
  box-shadow: 0 6px 16px rgba(91, 108, 240, 0.35);
}
.brand h1 { margin: 0; font-size: var(--text-2xl); font-weight: 700; letter-spacing: 0.2px; }
.slogan { margin: var(--space-2) 0 0; font-size: var(--text-sm); color: var(--text-2); }

/* ---- 分段控件：灰底轨道 + 白色滑块式选中项 ---- */
.segment {
  display: flex;
  gap: var(--space-1);
  background: var(--bg-hover);
  border-radius: var(--radius-md);
  padding: var(--space-1);
  margin-bottom: var(--space-5);
}
.segment-item {
  flex: 1;
  border: none;
  background: transparent;
  border-radius: var(--radius-sm);
  padding: 7px 0;
  font-size: var(--text-base);
  color: var(--text-2);
  transition: all var(--dur-base) var(--ease-out);
}
.segment-item:hover:not(.on) { color: var(--text-1); background: transparent; }
.segment-item.on {
  background: var(--bg-card);
  color: var(--color-primary);
  font-weight: 600;
  box-shadow: var(--shadow-bubble);
}

/* ---- 表单字段 ---- */
.field { display: block; margin-bottom: var(--space-4); }
.field-label {
  display: block;
  font-size: var(--text-sm);
  font-weight: 500;
  color: var(--text-2);
  margin-bottom: var(--space-2);
}
.field input { width: 100%; padding: 9px 12px; }

/* 昵称字段展开/收起过渡 */
.slide-enter-active, .slide-leave-active { transition: all var(--dur-base) var(--ease-out); }
.slide-enter-from, .slide-leave-to { opacity: 0; transform: translateY(-6px); }

/* ---- 错误提示 ---- */
.error {
  display: flex;
  align-items: center;
  gap: 6px;
  color: var(--color-danger);
  background: var(--color-danger-soft);
  border: 1px solid rgba(229, 72, 77, 0.25);
  border-radius: var(--radius-sm);
  font-size: var(--text-sm);
  margin: 0 0 var(--space-4);
  padding: 8px 10px;
  animation: shake 0.3s var(--ease-out);
}
@keyframes shake {
  0%, 100% { transform: translateX(0); }
  25% { transform: translateX(-3px); }
  75% { transform: translateX(3px); }
}

/* ---- 提交按钮：主色渐变 + loading 转圈 ---- */
.submit {
  width: 100%;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  border: none;
  color: #fff;
  font-size: var(--text-base);
  font-weight: 600;
  letter-spacing: 2px;
  padding: 10px;
  border-radius: var(--radius-md);
  background: linear-gradient(135deg, var(--color-primary), var(--color-primary-hover));
  box-shadow: 0 4px 12px rgba(91, 108, 240, 0.30);
  transition: filter var(--dur-fast) var(--ease-out), box-shadow var(--dur-fast) var(--ease-out), transform var(--dur-fast) var(--ease-out);
}
.submit:hover:not(:disabled) { background: linear-gradient(135deg, var(--color-primary), var(--color-primary-hover)); filter: brightness(1.05); }
.submit:active:not(:disabled) { transform: translateY(1px); }
.submit:disabled { box-shadow: none; }

/* CSS 实现的 loading 转圈，无需引入图标库 */
.spinner {
  width: 14px; height: 14px;
  border: 2px solid rgba(255, 255, 255, 0.4);
  border-top-color: #fff;
  border-radius: 50%;
  animation: spin 0.7s linear infinite;
}
@keyframes spin { to { transform: rotate(360deg); } }

.tip { margin: var(--space-4) 0 0; text-align: center; font-size: var(--text-sm); color: var(--text-3); }
.toggle { cursor: pointer; font-weight: 500; }

/* 窄屏：卡片占满宽度并压缩内边距 */
@media (max-width: 480px) {
  .login-wrap { padding: 0; align-items: stretch; }
  .card {
    width: 100%;
    border-radius: 0;
    border: none;
    min-height: 100%;
    padding: 48px var(--space-5) var(--space-6);
  }
}
</style>
