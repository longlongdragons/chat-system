<template>
  <router-view />
</template>

<script setup lang="ts">
/**
 * 应用根组件。
 *
 * 挂载时完成全局启动流程：
 * 1. 注册令牌生命周期监听器（刷新成功后持久化新令牌；彻底失效则登出并回登录页）；
 * 2. 从 localStorage 恢复登录态（auth.bootstrap）；
 * 3. 已登录则初始化聊天 store（拉会话列表并建立 WebSocket 长连接）。
 */
import { onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { setTokenListeners } from '@/api/http'
import { useChatStore } from '@/stores/chat'

const auth = useAuthStore()
const chat = useChatStore()
const router = useRouter()

onMounted(async () => {
  // 刷新成功 → 只更新内存令牌并写回 localStorage；
  // 刷新失败 → 本地登出、拆除聊天连接（WS/订阅），回到登录页
  setTokenListeners(
    (access) => { auth.accessToken = access; localStorage.setItem('chat.access', access) },
    () => { auth.logoutLocal(); chat.teardown(); router.push({ name: 'login' }) },
  )
  await auth.bootstrap()
  // 仅在恢复出有效登录态时才初始化聊天（拉会话 + 建 WebSocket）
  if (auth.isLoggedIn) await chat.init()
})
</script>