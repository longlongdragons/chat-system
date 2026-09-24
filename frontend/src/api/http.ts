/**
 * HTTP 客户端封装（axios 实例）。
 *
 * 职责：
 * - 统一携带后端 REST API（cmd/api 无状态服务）的访问令牌（Bearer Token）；
 * - 实现 token 过期后的自动刷新与请求重放，并保证并发请求场景下
 *   刷新操作只执行一次（单飞行 single-flight）；
 * - 刷新彻底失败时清空本地凭证并通知上层跳回登录页。
 *
 * 令牌保存在模块级变量中（由 auth store 通过 setTokens 同步），
 * 避免每次请求都读 localStorage，也便于拦截器统一取用。
 */
import axios, { type AxiosInstance, type AxiosRequestConfig } from 'axios'

/** 后端统一响应信封：code 为业务错误码，message 为可读信息，data 为业务数据 */
export interface Envelope<T = any> {
  code: string
  message?: string
  data?: T
}

const http: AxiosInstance = axios.create({
  baseURL: '/api/v1',
  timeout: 15000,
})

let accessToken: string | null = null
let refreshToken: string | null = null
// 刷新成功后回调：让 auth store 同步新令牌并持久化
let onTokenRefreshed: ((access: string, refresh: string) => void) | null = null
// 刷新失败（令牌彻底失效）时回调：让上层清理会话并跳转登录页
let onUnauthorized: (() => void) | null = null

/**
 * 设置/更新内存中的双令牌。
 * 传 null 表示清空（登出或令牌失效时调用）。
 */
export function setTokens(access: string | null, refresh: string | null) {
  accessToken = access
  refreshToken = refresh
}

/**
 * 注册令牌生命周期回调：刷新成功时的持久化钩子与彻底失效时的登出钩子。
 * 由 App.vue 挂载时调用，避免 http 模块反向依赖 store 形成循环引用。
 */
export function setTokenListeners(
  refreshed: (access: string, refresh: string) => void,
  unauthorized: () => void,
) {
  onTokenRefreshed = refreshed
  onUnauthorized = unauthorized
}

// 请求拦截器：有访问令牌时统一附带 Authorization 头
http.interceptors.request.use((config) => {
  if (accessToken) {
    config.headers = config.headers ?? {}
    ;(config.headers as any).Authorization = `Bearer ${accessToken}`
  }
  return config
})

// 进行中的刷新 Promise，非 null 表示已有刷新在途（单飞行标记）
let refreshing: Promise<string> | null = null

/**
 * 调用后端 /auth/refresh 换新双令牌。
 * 注意这里用的是裸 axios 而非上面的 http 实例，
 * 避免刷新请求本身再触发 401 拦截器造成递归。
 */
async function doRefresh(): Promise<string> {
  if (!refreshToken) throw new Error('no refresh token')
  const res = await axios.post<Envelope<{ access_token: string; refresh_token: string }>>(
    '/api/v1/auth/refresh',
    { refresh_token: refreshToken },
  )
  const data = res.data.data!
  setTokens(data.access_token, data.refresh_token)
  onTokenRefreshed?.(data.access_token, data.refresh_token)
  return data.access_token
}

// 响应拦截器：401 时自动刷新令牌并重放原请求
http.interceptors.response.use(
  (res) => res,
  async (error) => {
    // _retry 标记防止同一个请求被无限重放（刷新后仍 401 就直接失败）
    const original = error.config as AxiosRequestConfig & { _retry?: boolean }
    if (error.response?.status === 401 && !original._retry && refreshToken) {
      original._retry = true
      try {
        // 单飞行：并发的 401 请求共享同一个刷新 Promise，
        // 避免多个请求同时刷新导致后发的 refresh_token 因旧令牌已轮换而作废
        refreshing = refreshing ?? doRefresh()
        const token = await refreshing
        refreshing = null
        // 换上新令牌重放原请求，对业务调用方完全透明
        original.headers = original.headers ?? {}
        ;(original.headers as any).Authorization = `Bearer ${token}`
        return http(original)
      } catch (e) {
        // 刷新失败（refresh_token 也过期/被吊销）：清空凭证并通知上层登出
        refreshing = null
        setTokens(null, null)
        onUnauthorized?.()
        return Promise.reject(e)
      }
    }
    return Promise.reject(error)
  },
)

export default http