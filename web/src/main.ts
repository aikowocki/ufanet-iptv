import '@mdi/font/css/materialdesignicons.css'
import 'vuetify/styles'
import { createApp } from 'vue'
import { createVuetify } from 'vuetify'
import App from './App.vue'
import { router } from './router'

const vuetify = createVuetify({
  theme: { defaultTheme: 'dark' },
})

createApp(App).use(router).use(vuetify).mount('#app')
