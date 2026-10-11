/**
 * 头像/昵称配色的共享工具（组合式函数）。
 *
 * 按用户 ID（或任意字符串种子）哈希取色，保证同一用户在所有视图（会话列表、
 * 消息气泡、群成员抽屉、联系人、资料卡）中颜色一致；深色主题使用提亮版色盘，
 * 保证暗底可读性。颜色随主题切换实时响应（内部读取 theme store）。
 */
import { computed } from 'vue'
import { useThemeStore } from '@/stores/theme'

const PALETTE_LIGHT = ['#5b6cf0', '#0e9f8a', '#d97706', '#dc2626', '#7c3aed', '#db2777', '#0284c7', '#65a30d']
const PALETTE_DARK = ['#8b93f8', '#2dd4bf', '#fbbf24', '#f87171', '#a78bfa', '#f472b6', '#38bdf8', '#a3e635']

export function useAvatarColor() {
  const theme = useThemeStore()
  const palette = computed(() => (theme.mode === 'dark' ? PALETTE_DARK : PALETTE_LIGHT))

  /** 种子 → 色盘颜色（字符串哈希取模） */
  function colorOf(seed: number | string): string {
    const s = String(seed)
    let h = 0
    for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) >>> 0
    return palette.value[h % palette.value.length]
  }

  /** 文字头像样式：柔和底色（低透明度同色系）+ 彩色文字，深色下底色略浓 */
  function avatarStyle(seed: number | string) {
    const c = colorOf(seed)
    return { background: `${c}${theme.mode === 'dark' ? '33' : '26'}`, color: c }
  }

  /** 取昵称首字符作为头像占位（无头像图时的文字头像） */
  function initials(name?: string) {
    if (!name) return '?'
    return name.slice(0, 1).toUpperCase()
  }

  return { colorOf, avatarStyle, initials }
}
