/**
 * data-tip 悬停提示：跟随鼠标的自定义 tooltip（替代浏览器原生 title）。
 *
 * 单例实现：全局只有一个 .cursor-tip 元素挂在 body 下——
 * - mouseover 决定显示/隐藏/文案（事件委托，Vue 动态渲染的元素无需手动绑定）
 * - mousemove 负责跟随定位（只改 left/top，不参与过渡，跟随即时无拖拽感）
 *
 * 不用 :hover::after 纯 CSS 方案：伪元素读不到光标坐标，且 :hover 链条
 * 在某些环境下难以排查。JS 单例行为完全确定、可测试。
 *
 * 视觉与动效定义在 style.css 的 .cursor-tip 规则中：
 * 0.22s 淡入 + 3px 轻移；出现带 0.15s 延迟（扫过一排图标不闪烁），移出立即消失。
 */
let tipEl: HTMLDivElement | null = null

function ensureEl(): HTMLDivElement {
  if (!tipEl) {
    tipEl = document.createElement('div')
    tipEl.className = 'cursor-tip'
    tipEl.setAttribute('role', 'tooltip')
    document.body.appendChild(tipEl)
  }
  return tipEl
}

/** 写入气泡位置，并钳制到视口内（气泡最大约 140px 宽、48px 高） */
function position(x: number, y: number) {
  if (!tipEl) return
  tipEl.style.left = `${Math.min(x, window.innerWidth - 140)}px`
  tipEl.style.top = `${Math.min(y, window.innerHeight - 48)}px`
}

export function setupTooltip() {
  // mouseover 在元素间移动时都会触发：命中 [data-tip] 则更新文案并显示，否则隐藏
  document.addEventListener(
    'mouseover',
    (e) => {
      const el = (e.target as Element | null)?.closest?.('[data-tip]')
      const text = el?.getAttribute('data-tip')?.trim()
      const tip = ensureEl()
      if (el && text) {
        tip.textContent = text
        position(e.clientX, e.clientY) // 立即就位，避免从旧位置跳过来
        tip.classList.add('show')
      } else {
        tip.classList.remove('show')
      }
    },
    { passive: true },
  )

  // 跟随光标：显示期间每次移动都更新位置
  document.addEventListener(
    'mousemove',
    (e) => {
      if (!tipEl?.classList.contains('show')) return
      position(e.clientX, e.clientY)
    },
    { passive: true },
  )

  // 鼠标直接移出窗口时兜底隐藏
  document.addEventListener(
    'mouseout',
    (e) => {
      if (!e.relatedTarget) tipEl?.classList.remove('show')
    },
    { passive: true },
  )
}
