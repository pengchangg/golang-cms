import type { ModelPermission, Principal, SystemPermission } from '../api/types'

export interface NavigationItem {
  key: string
  label: string
  path: string
  permission?: SystemPermission
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
    (item) => !item.permission || hasSystemPermission(principal, item.permission),
  )
}

export function hasModelPermission(principal: Principal, modelId: string, permission: ModelPermission) {
  return principal.model_permissions.some((grant) => grant.model_id === modelId && grant.permissions.includes(permission))
}
