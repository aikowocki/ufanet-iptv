import { createRouter, createWebHistory } from 'vue-router'
import { isAdmin, isLoggedIn } from './lib/auth'
import AdminChannelsView from './views/admin/AdminChannelsView.vue'
import AdminPackagesView from './views/admin/AdminPackagesView.vue'
import AdminUsersView from './views/admin/AdminUsersView.vue'
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
    { path: '/admin', redirect: '/admin/users' },
    { path: '/admin/users', name: 'admin-users', component: AdminUsersView, meta: { auth: true, admin: true } },
    { path: '/admin/channels', name: 'admin-channels', component: AdminChannelsView, meta: { auth: true, admin: true } },
    { path: '/admin/packages', name: 'admin-packages', component: AdminPackagesView, meta: { auth: true, admin: true } },
  ],
})

router.beforeEach((to) => {
  if (to.meta.auth && !isLoggedIn.value) return { name: 'login' }
  if (to.meta.admin && !isAdmin.value) return { name: 'channels' }
  if (to.name === 'login' && isLoggedIn.value) return { name: 'channels' }
})
