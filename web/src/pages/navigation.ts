import type { NavigationItem } from '../auth/permissions'

export const workspaceLinks: NavigationItem[] = [
  { key: 'users', label: '用户管理', path: '/users', permission: 'users.view' },
  { key: 'roles', label: '角色与权限', path: '/roles', permission: 'roles.view' },
  { key: 'models', label: '内容模型', path: '/models', permission: 'models.view' },
  { key: 'api-keys', label: 'API Keys', path: '/api-keys', permission: 'api_keys.view' },
  { key: 'audit', label: '审计日志', path: '/audit', permission: 'audit.view' },
]

export const navigation: NavigationItem[] = [
  { key: 'workspace', label: '工作台', path: '/' },
  ...workspaceLinks,
  { key: 'account', label: '当前会话', path: '/account' },
]
