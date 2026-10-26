import { createRouter, createWebHistory } from 'vue-router'
import { hasValidSession } from '../auth'
import AdminUsers from '../views/AdminUsers.vue'
import AuditLogs from '../views/AuditLogs.vue'
import APIKeys from '../views/APIKeys.vue'
import Certificates from '../views/Certificates.vue'
import Settings from '../views/Settings.vue'
import Traefik from '../views/Traefik.vue'
import Users from '../views/Users.vue'
import Dashboard from '../views/Dashboard.vue'
import Login from '../views/Login.vue'

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes: [
    { path: '/', redirect: '/dashboard' },
    {
      path: '/login',
      name: 'login',
      component: Login,
      meta: { public: true },
    },
    {
      path: '/dashboard',
      name: 'dashboard',
      component: Dashboard,
    },
    {
      path: '/audit',
      name: 'audit',
      component: AuditLogs,
    },
    {
      path: '/admin/users',
      name: 'admin-users',
      component: AdminUsers,
    },
    {
      path: '/certificates',
      name: 'certificates',
      component: Certificates,
    },
    { path: '/traefik', name: 'traefik', component: Traefik },
    { path: '/users', name: 'users', component: Users },
    { path: '/api-keys', name: 'api-keys', component: APIKeys },
    { path: '/settings', name: 'settings', component: Settings },
    { path: '/:pathMatch(.*)*', redirect: '/dashboard' },
  ],
})

router.beforeEach((to) => {
  if (to.meta.public) {
    if (hasValidSession()) return { name: 'dashboard' }
    return true
  }
  if (!hasValidSession()) {
    return { name: 'login', query: { redirect: to.fullPath } }
  }
  return true
})

export default router
