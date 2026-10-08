<template>
  <!-- 主题切换按钮：太阳 / 月亮图标旋转交替 -->
  <button
    class="theme-toggle"
    :data-tip="theme.mode === 'dark' ? '切换到浅色主题' : '切换到深色主题'"
    :aria-label="theme.mode === 'dark' ? '切换到浅色主题' : '切换到深色主题'"
    @click="theme.toggle()"
  >
    <transition name="spin" mode="out-in">
      <!-- 月亮（深色模式下显示，点击切回浅色） -->
      <svg v-if="theme.mode === 'dark'" key="moon" viewBox="0 0 24 24" width="16" height="16" fill="none">
        <path
          d="M20.6 14.2A8.5 8.5 0 0 1 9.8 3.4a.6.6 0 0 0-.8-.7 10 10 0 1 0 12.3 12.3.6.6 0 0 0-.7-.8Z"
          fill="currentColor"
        />
      </svg>
      <!-- 太阳（浅色模式下显示，点击切到深色） -->
      <svg v-else key="sun" viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round">
        <circle cx="12" cy="12" r="4.2" fill="currentColor" stroke="none" />
        <path d="M12 2.5v2.2M12 19.3v2.2M2.5 12h2.2M19.3 12h2.2M4.9 4.9l1.6 1.6M17.5 17.5l1.6 1.6M19.1 4.9l-1.6 1.6M6.5 17.5l-1.6 1.6" />
      </svg>
    </transition>
  </button>
</template>

<script setup lang="ts">
/** 浅色/深色主题一键切换按钮，逻辑在 theme store，读写 <html data-theme> 并持久化 */
import { useThemeStore } from '@/stores/theme'

const theme = useThemeStore()
</script>

<style scoped>
.theme-toggle {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 30px;
  height: 30px;
  padding: 0;
  border-radius: var(--radius-sm);
  color: var(--text-2);
  overflow: hidden; /* 图标旋转进出时裁掉越界部分 */
}
.theme-toggle:hover:not(:disabled) { color: var(--color-primary); }

/* 图标切换：旧图标旋出、新图标旋入（尊重减少动效偏好） */
@media (prefers-reduced-motion: no-preference) {
  .spin-enter-active, .spin-leave-active {
    transition: transform var(--dur-base) var(--ease-out), opacity var(--dur-base) var(--ease-out);
  }
}
.spin-enter-from { transform: rotate(-120deg) scale(0.5); opacity: 0; }
.spin-leave-to { transform: rotate(120deg) scale(0.5); opacity: 0; }
</style>
