import { Button, Checkbox, Form, Input, List, Modal, Typography, message } from 'antd'
import { useState } from 'react'

import { api } from '../api/client'
import { modelPermissionCodes, systemPermissionCodes, type ModelPermission, type Principal, type Role, type SystemPermission } from '../api/types'
import { hasSystemPermission } from '../auth/permissions'
import { DataState, PageHeader, PendingApiNotice, useApiData } from '../components/Page'

export default function RolesPage({ principal }: { principal: Principal }) {
  const roles = useApiData(api.listRoles)
  const models = useApiData(() => api.listModels('active'))
  const [selected, setSelected] = useState<Role>()
  const canManage = hasSystemPermission(principal, 'roles.manage')
  async function saveSystem() {
    if (!selected) return
    setSelected(await api.replaceSystemPermissions(selected.id, selected.system_permissions))
    roles.reload(); message.success('系统权限已原子替换')
  }
  async function saveModels() {
    if (!selected) return
    setSelected(await api.replaceModelPermissions(selected.id, selected.model_permissions))
    roles.reload(); message.success('模型权限已原子替换')
  }
  function setGrant(modelId: string, permissions: ModelPermission[]) {
    if (!selected) return
    const grants = selected.model_permissions.filter((grant) => grant.model_id !== modelId)
    if (permissions.length) grants.push({ model_id: modelId, permissions })
    setSelected({ ...selected, model_permissions: grants })
  }
  return <><PageHeader eyebrow="访问控制" title="角色和权限矩阵" description="权限默认拒绝，系统权限和模型权限分别以完整集合原子替换。" /><PendingApiNotice /><DataState loading={roles.loading || models.loading} error={roles.error ?? models.error} empty={!roles.data?.items.length} retry={() => { roles.reload(); models.reload() }}><div className="split-workspace"><List className="selection-list" dataSource={roles.data?.items} renderItem={(role) => <List.Item onClick={() => setSelected(role)} className={selected?.id === role.id ? 'is-selected' : ''}><List.Item.Meta title={role.display_name} description={<><code>{role.key}</code><br/>{role.description || '无说明'}</>} /></List.Item>} /><section className="permission-matrix">{selected ? <><Typography.Title level={2}>{selected.display_name}</Typography.Title><Typography.Title level={3}>系统权限</Typography.Title><Checkbox.Group disabled={!canManage} value={selected.system_permissions} onChange={(values) => setSelected({ ...selected, system_permissions: values as SystemPermission[] })}>{systemPermissionCodes.map((code) => <label className="permission-cell" key={code}><Checkbox value={code} /><span>{code}</span></label>)}</Checkbox.Group><Button onClick={saveSystem} disabled={!canManage}>保存系统权限</Button><Typography.Title className="matrix-section" level={3}>模型内容权限</Typography.Title>{models.data?.items.map((model) => <section className="model-grant" key={model.id}><Typography.Text strong>{model.display_name}</Typography.Text><Checkbox.Group disabled={!canManage} options={modelPermissionCodes.map((code) => ({ label: code, value: code }))} value={selected.model_permissions.find((grant) => grant.model_id === model.id)?.permissions ?? []} onChange={(values) => setGrant(model.id, values as ModelPermission[])} /></section>)}<Button type="primary" onClick={saveModels} disabled={!canManage}>保存模型权限</Button></> : <Typography.Text type="secondary">选择角色查看权限矩阵</Typography.Text>}</section></div></DataState><CreateRoleButton disabled={!canManage} onCreated={roles.reload} /></>
}

function CreateRoleButton({ disabled, onCreated }: { disabled: boolean; onCreated: () => void }) {
  const [open, setOpen] = useState(false)
  const [form] = Form.useForm()
  return <><Button className="floating-action" type="primary" disabled={disabled} onClick={() => setOpen(true)}>新建角色</Button><Modal title="新建无授权角色" open={open} onCancel={() => setOpen(false)} onOk={() => form.validateFields().then(api.createRole).then(() => { setOpen(false); form.resetFields(); onCreated() })}><Form form={form} layout="vertical"><Form.Item name="key" label="稳定标识" rules={[{ required: true }, { pattern: /^[a-z][a-z0-9_]{0,63}$/ }]}><Input /></Form.Item><Form.Item name="display_name" label="显示名称" rules={[{ required: true }]}><Input /></Form.Item><Form.Item name="description" label="说明"><Input.TextArea /></Form.Item></Form></Modal></>
}
