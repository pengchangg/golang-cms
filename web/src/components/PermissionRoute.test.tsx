import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, expect, it } from 'vitest'

import type { Principal } from '../api/types'
import { apiKeyPagePermissions, rolePagePermissions } from '../pages/navigation'
import { PermissionRoute } from './PermissionRoute'

const principal = (permission: 'roles.manage' | 'api_keys.create' | 'api_keys.revoke'): Principal => ({ user_id: 'usr_1', display_name: '管理员', email: null, auth_method: 'local', is_emergency_admin: false, has_high_risk_role: false, system_permissions: [permission], model_permissions: [] })

afterEach(cleanup)

it.each([
  ['roles.manage', rolePagePermissions],
  ['api_keys.create', apiKeyPagePermissions],
  ['api_keys.revoke', apiKeyPagePermissions],
] as const)('%s 可通过对应页面的 OR 权限路由', (permission, permissions) => {
  render(<PermissionRoute principal={principal(permission)} system={permissions}><div>页面内容</div></PermissionRoute>)
  expect(screen.getByText('页面内容')).toBeVisible()
  expect(screen.queryByText('无权访问')).not.toBeInTheDocument()
})
