import { createApp } from 'vue'
import App from './App.vue'
import router from './router'
import { installI18n } from './i18n'
import './style.css'

createApp(App).use(router).mount('#app')
installI18n()
