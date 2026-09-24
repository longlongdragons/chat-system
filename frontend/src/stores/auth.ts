/**
 * 认证状态（Pinia store）。
 *
 * 职责：
 * - 保存当前用户与 access/refresh token，并持久化到 localStorage（刷新页面后可恢复登录态）；
 * - 维护设备 ID（首次访问生成并持久化），登录/注册时上报给服务端用于多端会话管理；
 * - token 变更时同步到 HTTP 层（setTokens），供 axios 拦截器携带与刷新；
 * - bootstrap 时用本地 token 拉取用户信息校验登录态，失败则本地登出。
 */
import { defineStore } from 'pinia'
import { api, type User } from '@/api'
import { setTokens } from '@/api/http'

const ACCESS_KEY = 'chat.access'
const REFRESH_KEY = 'chat.refresh'
const DEVICE_KEY = 'chat.device'

/**
 * 设备 ID：同一浏览器持久不变，首次访问时生成。
 * 服务端用它区分同一账号的多端登录，管理各端的在线会话。
 */
function getDeviceId(): string {
  let id = localStorage.getItem(DEVICE_KEY)
  if (!id) {
    id = crypto.randomUUID()
    localStorage.setItem(DEVICE_KEY, id)
  }
  return id
}

export const useAuthStore = defineStore('auth', {
  state: () => ({
    user: null as User | null,
    accessToken: localStorage.getItem(ACCESS_KEY) as string | null,
    refreshToken: localStorage.getItem(REFRESH_KEY) as string | null,
    deviceId: getDeviceId(),
  }),

  getters: {
    isLoggedIn: (s) => !!s.user && !!s.accessToken,
  },

  actions: {
    /**
     * 应用并持久化 token：同步更新 store、localStorage 与 HTTP 层三处，
     * 传 null 表示清除（登出）。HTTP 层持有 token 供 axios 请求拦截器携带、刷新。
     */
    applyTokens(access: string | null, refresh: string | null) {
      this.accessToken = access
      this.refreshToken = refresh
      if (access) localStorage.setItem(ACCESS_KEY, access)
      else localStorage.removeItem(ACCESS_KEY)
      if (refresh) localStorage.setItem(REFRESH_KEY, refresh)
      else localStorage.removeItem(REFRESH_KEY)
      setTokens(access, refresh)
    },

    /**
     * 应用启动时恢复登录态：把 localStorage 里的 token 交给 HTTP 层，
     * 再调 me 接口校验 token 是否仍有效；无效则清空本地登录态。
     */
    async bootstrap() {
      setTokens(this.accessToken, this.refreshToken)
      if (!this.accessToken) return
      try {
        this.user = await api.me()
      } catch {
        this.logoutLocal()
      }
    },

    async login(login: string, password: string) {
      const res = await api.login({ login, password, device_id: this.deviceId })
      this.user = res.user
      this.applyTokens(res.access_token, res.refresh_token)
    },

    async register(payload: { username?: string; email?: string; phone?: string; password: string; nickname?: string }) {
      const res = await api.register({ ...payload, device_id: this.deviceId })
      this.user = res.user
      this.applyTokens(res.access_token, res.refresh_token)
    },

    /** 登出：尽力通知服务端使 refresh token 失效（失败也继续），然后清理本地登录态 */
    async logout() {
      try {
        await api.logout()
      } catch { /* ignore */ }
      this.logoutLocal()
    },

    logoutLocal() {
      this.user = null
      this.applyTokens(null, null)
    },
  },
})