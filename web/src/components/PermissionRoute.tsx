import { Result } from 'antd'
import type { ReactNode } from 'react'

import type { ConfigNamespacePermission, ModelPermission, Principal, SystemPermission } from '../api/types'
import { hasConfigNamespacePermission, hasModelPermission, hasSystemPermission } from '../auth/permissions'

export function PermissionRoute({ principal, system, model, configNamespace, children }: { principal: Principal; system?: SystemPermission | readonly SystemPermission[]; model?: { id: string; permission: ModelPermission }; configNamespace?: { id: string; permission: ConfigNamespacePermission }; children: ReactNode }) {
  const allowed = system ? (typeof system === 'string' ? hasSystemPermission(principal, system) : system.some((permission) => hasSystemPermission(principal, permission))) : model ? hasModelPermission(principal, model.id, model.permission) : configNamespace ? hasConfigNamespacePermission(principal, configNamespace.id, configNamespace.permission) : true
  return allowed ? children : <Result status="403" title="无权访问" subTitle="当前会话没有此页面所需权限。" />
}
