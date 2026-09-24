/**
 * 前端路由配置与全局导航守卫。
 *
 * 页面只有登录页与聊天主页两个；守卫负责：
 * - 未登录访问受保护页面时重定向到登录页，并记录 redirect 参数以便登录后回跳；
 * - 已登录用户访问登录页时直接进入聊天页，避免重复登录。
 */
import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', redirect: '/chat' },
    { path: '/login', name: 'login', component: () => import('@/views/LoginView.vue') },
    {
      path: '/chat',
      name: 'chat',
      component: () => import('@/views/ChatView.vue'),
      meta: { requiresAuth: true },
    },
  ],
})

router.beforeEach((to) => {
  const auth = useAuthStore()
  // 未持有访问令牌却访问受保护路由：跳登录页并带上回跳地址
  if (to.meta.requiresAuth && !auth.accessToken) {
    return { name: 'login', query: { redirect: to.fullPath } }
  }
  // 已登录用户直接绕过登录页
  if (to.name === 'login' && auth.accessToken && auth.user) {
    return { name: 'chat' }
  }
  return true
})

export default router