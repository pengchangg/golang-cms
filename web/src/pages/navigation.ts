import type { NavigationItem } from '../auth/permissions'
import { ASSETS_ENABLED, CONTENT_API_EXPLORER_ENABLED } from '../config'

export const rolePagePermissions = ['roles.view', 'roles.manage'] as const
export const apiKeyPagePermissions = ['api_keys.view', 'api_keys.create', 'api_keys.revoke'] as const

export const workspaceLinks: NavigationItem[] = ([
  { key: 'users', label: '用户管理', path: '/users', permission: 'users.view' },
  { key: 'roles', label: '角色与权限', path: '/roles', permission: rolePagePermissions },
  { key: 'models', label: '内容模型', path: '/models', permission: 'models.view' },
  { key: 'configurations', label: '配置管理', path: '/configurations', permission: 'configurations.view' },
  { key: 'assets', label: '素材库', path: '/assets', permission: 'assets.view' },
  { key: 'api-keys', label: 'API Keys', path: '/api-keys', permission: apiKeyPagePermissions },
  { key: 'api-explorer', label: '客户端调试', path: '/api-explorer', permission: apiKeyPagePermissions },
  { key: 'audit', label: '审计日志', path: '/audit', permission: 'audit.view' },
] satisfies NavigationItem[]).filter((item) => {
  if (!ASSETS_ENABLED && item.key === 'assets') return false
  if (!CONTENT_API_EXPLORER_ENABLED && item.key === 'api-explorer') return false
  return true
})

export const navigation: NavigationItem[] = [
  { key: 'workspace', label: '工作台', path: '/' },
  ...workspaceLinks,
  { key: 'account', label: '当前会话', path: '/account' },
]
