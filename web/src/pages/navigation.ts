import type { NavigationItem } from '../auth/permissions'
import { ASSETS_ENABLED } from '../config'

export const workspaceLinks: NavigationItem[] = ([
  { key: 'users', label: '用户管理', path: '/users', permission: 'users.view' },
  { key: 'roles', label: '角色与权限', path: '/roles', permission: 'roles.view' },
  { key: 'models', label: '内容模型', path: '/models', permission: 'models.view' },
  { key: 'assets', label: '素材库', path: '/assets', permission: 'assets.view' },
  { key: 'api-keys', label: 'API Keys', path: '/api-keys', permission: 'api_keys.view' },
  { key: 'audit', label: '审计日志', path: '/audit', permission: 'audit.view' },
] satisfies NavigationItem[]).filter((item) => ASSETS_ENABLED || item.key !== 'assets')

export const navigation: NavigationItem[] = [
  { key: 'workspace', label: '工作台', path: '/' },
  ...workspaceLinks,
  { key: 'account', label: '当前会话', path: '/account' },
]
