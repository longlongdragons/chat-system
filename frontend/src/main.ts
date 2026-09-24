/**
 * 应用入口：创建 Vue 应用，挂载 Pinia（状态管理）与 vue-router，渲染到 #app。
 * 全局启动逻辑（恢复登录态、初始化聊天连接）在 App.vue 的 onMounted 中完成。
 */
import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import router from './router'
import './style.css'

const app = createApp(App)
app.use(createPinia())
app.use(router)
app.mount('#app')