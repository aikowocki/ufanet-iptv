import { createRouter, createWebHistory } from 'vue-router'
import { isLoggedIn } from './lib/auth'
import ChannelsView from './views/ChannelsView.vue'
import LoginView from './views/LoginView.vue'
import SubscriptionsView from './views/SubscriptionsView.vue'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', redirect: '/channels' },
    { path: '/login', name: 'login', component: LoginView },
    { path: '/channels', name: 'channels', component: ChannelsView, meta: { auth: true } },
    { path: '/subscriptions', name: 'subscriptions', component: SubscriptionsView, meta: { auth: true } },
  ],
})

router.beforeEach((to) => {
  if (to.meta.auth && !isLoggedIn.value) return { name: 'login' }
  if (to.name === 'login' && isLoggedIn.value) return { name: 'channels' }
})
