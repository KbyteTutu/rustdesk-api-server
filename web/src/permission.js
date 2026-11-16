import { router } from '@/router'
import { useUserStore } from '@/store/user'
import { getToken } from '@/utils/auth'
import { pinia } from '@/store'
import NProgress from 'nprogress'
import 'nprogress/nprogress.css'
import { useAppStore } from '@/store/app'
import { T } from '@/utils/i18n'

NProgress.configure({ showSpinner: false })
const whiteList = ['/login', '/register']
const appStore = useAppStore(pinia)
appStore.getAdminConfig()
router.beforeEach(async (to) => {
  document.title = `${T(to.meta?.title)} - ${appStore.setting.title}`
  NProgress.start()
  const loginRoute = { path: '/login', query: { redirect: to.fullPath } }
  if (getToken()) {
    const userStore = useUserStore(pinia)
    if (userStore.route_names.length) return true
    const info = await userStore.info()
    if (info) return { ...to, replace: true }
    userStore.clearSession()
    return loginRoute
  }
  return whiteList.includes(to.path) ? true : loginRoute
})
router.afterEach(() => NProgress.done())
