<template>
  <!-- emoji 快捷输入面板：静态分组网格，点击即向输入框插入（不引第三方库） -->
  <div class="emoji-panel" role="dialog" aria-label="表情面板">
    <div v-for="g in GROUPS" :key="g.name" class="emoji-group">
      <div class="group-name">{{ g.name }}</div>
      <div class="grid">
        <button
          v-for="e in g.emojis"
          :key="e"
          type="button"
          class="emoji-cell"
          :title="e"
          @click="emit('pick', e)"
        >
          {{ e }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
/**
 * Emoji 选择面板。
 * 内置约 100 个高频 emoji，按「笑脸 / 手势 / 心情 / 物品与动物」分组；
 * 点击通过 pick 事件交给父组件插入输入框光标处。
 */
const emit = defineEmits<{ pick: [emoji: string] }>()

const GROUPS: Array<{ name: string; emojis: string[] }> = [
  {
    name: '笑脸',
    emojis: [
      '😀', '😄', '😁', '🤣', '😂', '🙂', '😉', '😊', '😍', '😘',
      '😜', '🤪', '😝', '🤗', '🤔', '🤨', '😐', '😴', '😷', '🤯',
      '😎', '🥳', '😇', '🤠', '🥺', '😢', '😭', '😤', '😱', '🥰',
    ],
  },
  {
    name: '手势',
    emojis: [
      '👍', '👎', '👌', '✌️', '🤞', '🤟', '🤘', '👏', '🙌', '🙏',
      '👋', '🤝', '💪', '☝️', '👆', '👇', '👈', '👉', '✋', '🫶',
    ],
  },
  {
    name: '心情',
    emojis: [
      '❤️', '🧡', '💛', '💚', '💙', '💜', '🖤', '🤍', '💔', '💕',
      '💖', '💗', '💯', '✨', '🔥', '🎉', '🎊', '💤', '💥', '🌈',
      '⭐', '🌟', '⚡', '☀️', '🌙', '☁️',
    ],
  },
  {
    name: '物品与动物',
    emojis: [
      '🎁', '📱', '💻', '⌚', '📷', '🎧', '☕', '🍺', '🍰', '🍉',
      '⚽', '🏀', '🚀', '✈️', '🚗', '🏠', '🐱', '🐶', '🐼', '🐰',
      '🦄', '🐳', '🌹', '🍀',
    ],
  },
]
</script>

<style scoped>
.emoji-panel {
  width: 320px;
  max-height: 300px;
  overflow-y: auto;
  padding: var(--space-3);
  background: var(--bg-glass-card);
  backdrop-filter: blur(18px) saturate(1.3);
  -webkit-backdrop-filter: blur(18px) saturate(1.3);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-pop);
}
.group-name {
  font-size: var(--text-xs);
  color: var(--text-3);
  margin: var(--space-2) 0 var(--space-1);
}
.emoji-group:first-child .group-name { margin-top: 0; }
.grid { display: grid; grid-template-columns: repeat(8, 1fr); gap: 2px; }
.emoji-cell {
  border: none;
  background: transparent;
  padding: 4px 0;
  font-size: 19px;
  line-height: 1.2;
  border-radius: var(--radius-sm);
  transition: background var(--dur-fast) var(--ease-out), transform var(--dur-fast) var(--ease-out);
}
.emoji-cell:hover { background: var(--bg-hover); transform: scale(1.15); }
</style>
