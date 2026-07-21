import { describe, expect, it } from 'vitest'

import type { Principal } from '../api/types'
import { hasModelPermission, hasSystemPermission, visibleNavigation } from './permissions'

const principal: Principal = {
  user_id: 'usr_permissions',
  display_name: '权限测试用户',
  email: null,
  auth_method: 'sms',
  system_permissions: ['models.view'],
  model_permissions: [],
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

  it('模型内容权限按目标模型默认拒绝', () => {
    const withGrant = { ...principal, model_permissions: [{ model_id: 'mdl_news', permissions: ['content.view' as const] }] }
    expect(hasModelPermission(withGrant, 'mdl_news', 'content.view')).toBe(true)
    expect(hasModelPermission(withGrant, 'mdl_other', 'content.view')).toBe(false)
    expect(hasModelPermission(withGrant, 'mdl_news', 'content.update')).toBe(false)
  })
})
