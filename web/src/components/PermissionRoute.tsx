import { Result } from 'antd'
import type { ReactNode } from 'react'

import type { ModelPermission, Principal, SystemPermission } from '../api/types'
import { hasModelPermission, hasSystemPermission } from '../auth/permissions'

export function PermissionRoute({ principal, system, model, children }: { principal: Principal; system?: SystemPermission; model?: { id: string; permission: ModelPermission }; children: ReactNode }) {
  const allowed = system ? hasSystemPermission(principal, system) : model ? hasModelPermission(principal, model.id, model.permission) : true
  return allowed ? children : <Result status="403" title="无权访问" subTitle="当前会话没有此页面所需权限。" />
}
