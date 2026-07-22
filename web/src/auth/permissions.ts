import type { ModelPermission, Principal, SystemPermission } from '../api/types'
import type { ReactNode } from 'react'

export interface NavigationItem {
  key: string
  label: ReactNode
  textLabel?: string
  path: string
  permission?: SystemPermission | readonly SystemPermission[]
}

export function hasSystemPermission(
  principal: Principal,
  permission: SystemPermission,
) {
  return principal.system_permissions.includes(permission)
}

export function visibleNavigation(
  items: NavigationItem[],
  principal: Principal,
) {
  return items.filter(
    (item) => !item.permission || (typeof item.permission === 'string' ? hasSystemPermission(principal, item.permission) : item.permission.some((permission) => hasSystemPermission(principal, permission))),
  )
}

export function hasModelPermission(principal: Principal, modelId: string, permission: ModelPermission) {
  return principal.model_permissions.some((grant) => grant.model_id === modelId && grant.permissions.includes(permission))
}

export function canDelegateRole(principal: Principal, role: { system_permissions: SystemPermission[]; model_permissions: Array<{ model_id: string; permissions: ModelPermission[] }> }) {
  if (principal.is_emergency_admin || principal.has_high_risk_role) return true
  return role.system_permissions.every((permission) => hasSystemPermission(principal, permission))
    && role.model_permissions.every((grant) => grant.permissions.every((permission) => hasModelPermission(principal, grant.model_id, permission)))
}
