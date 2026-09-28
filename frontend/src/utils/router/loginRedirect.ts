import { RoutesAlias } from '@/router/routesAlias'

const ADMIN_APP_STORE_PATH = '/admin/app-store'

/**
 * 判断回跳地址是否属于独立应用商店页面。
 * 应用商店由 Go 静态入口处理，不能被管理后台动态路由接管。
 */
export function isAdminAppStorePath(path: string): boolean {
  return path === ADMIN_APP_STORE_PATH || path.startsWith(`${ADMIN_APP_STORE_PATH}/`)
}

/**
 * 清洗管理员登录后的回跳地址。
 *
 * 登录页本身、登录页的嵌套 redirect、面板入口、安装页和外部地址都不应
 * 再作为 redirect 传递，否则会不断生成 /admin?redirect=...。
 */
export function normalizeAdminLoginRedirect(value: unknown): string | undefined {
  if (typeof value !== 'string') return undefined

  const redirect = value.trim()
  if (!redirect) return undefined

  let decodedRedirect = redirect
  try {
    decodedRedirect = decodeURIComponent(redirect)
  } catch {
    return undefined
  }

  if (!decodedRedirect.startsWith('/') || decodedRedirect.startsWith('//')) {
    return undefined
  }

  const targetPath = decodedRedirect.split(/[?#]/, 1)[0]
  if (
    targetPath === '/' ||
    targetPath === '/install' ||
    targetPath.startsWith('/install/') ||
    targetPath === RoutesAlias.Login ||
    (targetPath.startsWith(`${RoutesAlias.Login}/`) && !isAdminAppStorePath(targetPath)) ||
    targetPath === '/user' ||
    targetPath.startsWith('/user/') ||
    targetPath === '/agent-panel' ||
    targetPath.startsWith('/agent-panel/')
  ) {
    return undefined
  }

  return redirect
}
