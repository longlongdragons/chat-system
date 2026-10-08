/**
 * 主题状态（Pinia store）：浅色 / 深色双主题切换。
 *
 * 工作机制：
 * - 当前主题写入 <html data-theme="...">，CSS 变量按 [data-theme="dark"] 选择器整体覆盖；
 * - 用户选择持久化到 localStorage（chat.theme）；
 * - 首次访问（无持久化值）跟随系统 prefers-color-scheme，且此时系统主题变化会实时跟随；
 *   一旦用户手动切换过，就以用户选择为准、不再跟随系统。
 *
 * index.html 里有一段内联引导脚本在应用启动前先写入 data-theme，避免刷新时主题闪烁（FOUC），
 * 本 store 的初始值探测逻辑与它保持一致。
 */
import { defineStore } from 'pinia'

export type ThemeMode = 'light' | 'dark'

const THEME_KEY = 'chat.theme'

function savedTheme(): ThemeMode | null {
  const v = localStorage.getItem(THEME_KEY)
  return v === 'light' || v === 'dark' ? v : null
}

/** 初始主题：优先用户持久化选择，否则跟随系统 */
function detectTheme(): ThemeMode {
  return savedTheme() ?? (window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light')
}

export const useThemeStore = defineStore('theme', {
  state: () => ({
    mode: detectTheme() as ThemeMode,
  }),

  actions: {
    /** 把当前主题应用到 <html data-theme>（启动时与切换时都会调用） */
    apply() {
      document.documentElement.dataset.theme = this.mode
    },

    /**
     * 应用启动时初始化：应用主题，并在"用户未手动选择过"时监听系统主题变化实时跟随。
     * 只会调用一次（App.vue onMounted）。
     */
    init() {
      this.apply()
      window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', (e) => {
        if (savedTheme()) return // 用户已手动选择过，不再跟随系统
        this.mode = e.matches ? 'dark' : 'light'
        this.apply()
      })
    },

    toggle() {
      this.mode = this.mode === 'dark' ? 'light' : 'dark'
      localStorage.setItem(THEME_KEY, this.mode)
      this.apply()
    },
  },
})
