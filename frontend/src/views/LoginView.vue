<template>
  <div class="login-wrap">
    <div class="card">
      <h1>{{ mode === 'login' ? '登录' : '注册' }}</h1>

      <form @submit.prevent="submit">
        <label>
          <span>账号</span>
          <input v-model.trim="form.login" placeholder="用户名 / 邮箱 / 手机号" required />
        </label>

        <label v-if="mode === 'register'">
          <span>昵称</span>
          <input v-model.trim="form.nickname" placeholder="可选" />
        </label>

        <label>
          <span>密码</span>
          <input v-model="form.password" type="password" placeholder="至少 6 位" required minlength="6" />
        </label>

        <p v-if="error" class="error">{{ error }}</p>

        <button type="submit" :disabled="loading">
          {{ loading ? '处理中…' : mode === 'login' ? '登录' : '注册并登录' }}
        </button>
      </form>

      <a class="toggle" @click="toggleMode">
        {{ mode === 'login' ? '没有账号？去注册' : '已有账号？去登录' }}
      </a>
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
import { reactive, ref } from 'vue'
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

function toggleMode() {
  mode.value = mode.value === 'login' ? 'register' : 'login'
  error.value = ''
}

/** 提交登录/注册表单；成功后初始化聊天连接并回跳目标页 */
async function submit() {
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
.login-wrap {
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
}
.card {
  width: 360px;
  background: #fff;
  border-radius: 12px;
  padding: 32px 28px;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.08);
}
h1 { margin: 0 0 24px; font-size: 22px; }
label { display: block; margin-bottom: 16px; }
label span { display: block; font-size: 13px; color: #57606a; margin-bottom: 6px; }
label input { width: 100%; }
button[type="submit"] {
  width: 100%;
  background: #0969da;
  color: #fff;
  border-color: #0969da;
  padding: 9px;
  margin-top: 4px;
}
button[type="submit"]:disabled { background: #8cb8e8; border-color: #8cb8e8; }
.error { color: #cf222e; font-size: 13px; margin: 0 0 12px; }
.toggle { display: block; margin-top: 16px; text-align: center; font-size: 13px; color: #0969da; cursor: pointer; }
</style>