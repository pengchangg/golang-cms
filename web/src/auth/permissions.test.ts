import { describe, expect, it } from 'vitest'

import type { Principal } from '../api/types'
import { apiKeyPagePermissions, rolePagePermissions, workspaceLinks } from '../pages/navigation'
import { canDelegateRole, hasModelPermission, hasSystemPermission, visibleNavigation } from './permissions'

const principal: Principal = {
  user_id: 'usr_permissions',
  display_name: '权限测试用户',
  email: null,
  auth_method: 'sms',
  is_emergency_admin: false,
  has_high_risk_role: false,
  system_permissions: ['models.view'],
  model_permissions: [],
  config_namespace_permissions: [],
}

describe('权限导航', () => {
  it('默认拒绝并只显示已获权限的入口', () => {
    expect(hasSystemPermission(principal, 'models.view')).toBe(true)
    expect(hasSystemPermission(principal, 'users.view')).toBe(false)
    expect(
      visibleNavigation(
        [
          { key: 'session', label: '当前会话', path: '/' },
          { key: 'models', label: '模型', path: '/models', permission: 'models.view' },
          { key: 'users', label: '用户', path: '/users', permission: 'users.view' },
        ],
        principal,
      ).map(({ key }) => key),
    ).toEqual(['session', 'models'])
  })

  it('普通主体只能委派自己的权限子集', () => {
    expect(canDelegateRole(principal, { system_permissions: ['models.view'], model_permissions: [], config_namespace_permissions: [] })).toBe(true)
    expect(canDelegateRole(principal, { system_permissions: ['audit.view'], model_permissions: [], config_namespace_permissions: [] })).toBe(false)
    expect(canDelegateRole({ ...principal, has_high_risk_role: true }, { system_permissions: ['audit.view'], model_permissions: [], config_namespace_permissions: [] })).toBe(true)
  })

  it('导航项支持任一相关系统权限', () => {
    expect(rolePagePermissions).toEqual(['roles.view', 'roles.manage'])
    expect(apiKeyPagePermissions).toEqual(['api_keys.view', 'api_keys.create', 'api_keys.revoke'])
    expect(visibleNavigation(workspaceLinks, { ...principal, system_permissions: ['roles.manage'] }).map(({ key }) => key)).toEqual(['roles'])
    expect(visibleNavigation(workspaceLinks, { ...principal, system_permissions: ['api_keys.revoke'] }).map(({ key }) => key)).toEqual(['api-keys', 'api-explorer'])
  })

  it('模型内容权限按目标模型默认拒绝', () => {
    const withGrant = { ...principal, model_permissions: [{ model_id: 'mdl_news', permissions: ['content.view' as const] }] }
    expect(hasModelPermission(withGrant, 'mdl_news', 'content.view')).toBe(true)
    expect(hasModelPermission(withGrant, 'mdl_other', 'content.view')).toBe(false)
    expect(hasModelPermission(withGrant, 'mdl_news', 'content.update')).toBe(false)
  })
})
