import { Button, Checkbox, Form, Input, List, Modal, Tag, Typography, message } from 'antd'
import { useState } from 'react'

import { api, apiErrorMessage } from '../api/client'
import { configNamespacePermissionCodes, modelPermissionCodes, systemPermissionCodes, type ConfigNamespacePermission, type ModelPermission, type Principal, type Role, type SystemPermission } from '../api/types'
import { hasSystemPermission } from '../auth/permissions'
import { DataState, PageHeader, PendingApiNotice, useApiData } from '../components/Page'

export default function RolesPage({ principal }: { principal: Principal }) {
  const canView = hasSystemPermission(principal, 'roles.view')
  const roles = useApiData(() => canView ? api.listRoles() : Promise.resolve({ items: [] }), [canView])
  const canViewModels = hasSystemPermission(principal, 'models.view')
  const models = useApiData(() => canViewModels ? api.listModels('active') : Promise.resolve({ items: [] }), [canViewModels])
  const canViewNamespaces = hasSystemPermission(principal, 'configurations.view')
  const namespaces = useApiData(() => canViewNamespaces ? api.listConfigurationNamespaces('active') : Promise.resolve({ items: [] }), [canViewNamespaces])
  const [selected, setSelected] = useState<Role>()
  const canManage = hasSystemPermission(principal, 'roles.manage')
  const selectedImmutable = selected?.kind === 'high_risk'
  const visibleModels = [
    ...(models.data?.items ?? []),
    ...(selected?.model_permissions.filter((grant) => !models.data?.items.some((model) => model.id === grant.model_id)).map((grant) => ({ id: grant.model_id, display_name: grant.model_id })) ?? []),
  ]
  const unavailableNamespaceGrants = selected?.config_namespace_permissions.filter((grant) => !namespaces.data?.items.some((namespace) => namespace.id === grant.config_namespace_id)) ?? []

  async function saveSystem() {
    if (!selected) return
    try {
      setSelected(await api.replaceSystemPermissions(selected.id, selected.system_permissions))
      roles.reload()
      message.success('系统权限已原子替换')
    } catch (error) {
      message.error(apiErrorMessage(error, '保存系统权限失败'))
    }
  }

  async function saveModels() {
    if (!selected) return
    try {
      setSelected(await api.replaceModelPermissions(selected.id, selected.model_permissions))
      roles.reload()
      message.success('模型权限已原子替换')
    } catch (error) {
      message.error(apiErrorMessage(error, '保存模型权限失败'))
    }
  }

  async function saveNamespaces() {
    if (!selected) return
    try {
      setSelected(await api.updateRoleConfigNamespacePermissions(selected.id, selected.config_namespace_permissions))
      roles.reload(); message.success('配置命名空间权限已原子替换')
    } catch (error) { message.error(apiErrorMessage(error, '保存配置命名空间权限失败')) }
  }

  function setGrant(modelId: string, permissions: ModelPermission[]) {
    if (!selected) return
    const grants = selected.model_permissions.filter((grant) => grant.model_id !== modelId)
    if (permissions.length) grants.push({ model_id: modelId, permissions })
    setSelected({ ...selected, model_permissions: grants })
  }
  function setNamespaceGrant(namespaceId: string, permissions: ConfigNamespacePermission[]) {
    if (!selected) return
    const grants = selected.config_namespace_permissions.filter((grant) => grant.config_namespace_id !== namespaceId)
    if (permissions.length) grants.push({ config_namespace_id: namespaceId, permissions })
    setSelected({ ...selected, config_namespace_permissions: grants })
  }

  return <>
    <PageHeader eyebrow="访问控制" title="角色和权限矩阵" description="权限默认拒绝，系统权限和模型权限分别以完整集合原子替换。" />
    <PendingApiNotice />
    <DataState loading={roles.loading || models.loading || namespaces.loading} error={roles.error ?? models.error ?? namespaces.error} empty={canView && !roles.data?.items.length} retry={() => { roles.reload(); models.reload(); namespaces.reload() }}>
      <div className="split-workspace">
        <List className="selection-list" dataSource={roles.data?.items} renderItem={(role) => <List.Item role="button" tabIndex={0} aria-pressed={selected?.id === role.id} onClick={() => setSelected(role)} onKeyDown={(event) => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); setSelected(role) } }} className={selected?.id === role.id ? 'is-selected' : ''}><List.Item.Meta title={<>{role.display_name}{role.kind === 'high_risk' ? <Tag color="red">内置高危</Tag> : null}</>} description={<><code>{role.key}</code><br/>{role.description || '无说明'}</>} /></List.Item>} />
        <section className="permission-matrix">
          {selected ? <>
            <Typography.Title level={2}>{selected.display_name}</Typography.Title>
            {selectedImmutable ? <Typography.Paragraph type="secondary">该内置角色动态拥有全部系统权限和 active 模型权限，不可编辑或删除。</Typography.Paragraph> : null}
            <Typography.Title level={3}>系统权限</Typography.Title>
            <Checkbox.Group disabled={!canManage || selectedImmutable} value={selected.system_permissions} onChange={(values) => setSelected({ ...selected, system_permissions: values as SystemPermission[] })}>
              {systemPermissionCodes.map((code) => <label className="permission-cell" key={code}><Checkbox value={code} /><span>{code}</span></label>)}
            </Checkbox.Group>
            <Button onClick={saveSystem} disabled={!canManage || selectedImmutable}>保存系统权限</Button>
            <Typography.Title className="matrix-section" level={3}>模型内容权限</Typography.Title>
            {visibleModels.map((model) => <section className="model-grant" key={model.id}><Typography.Text strong>{model.display_name}</Typography.Text><Checkbox.Group disabled={!canManage || selectedImmutable} options={modelPermissionCodes.map((code) => ({ label: code, value: code }))} value={selected.model_permissions.find((grant) => grant.model_id === model.id)?.permissions ?? []} onChange={(values) => setGrant(model.id, values as ModelPermission[])} /></section>)}
            <Button onClick={saveModels} disabled={!canManage || selectedImmutable}>保存模型权限</Button>
            <Typography.Title className="matrix-section" level={3}>配置命名空间权限</Typography.Title>
            {(namespaces.data?.items ?? []).map((namespace) => <section className="model-grant" key={namespace.id}><Typography.Text strong>{namespace.display_name} <code>{namespace.namespace_key}</code></Typography.Text><Checkbox.Group disabled={!canManage || selectedImmutable} options={configNamespacePermissionCodes.map((code) => ({ label: code, value: code }))} value={selected.config_namespace_permissions.find((grant) => grant.config_namespace_id === namespace.id)?.permissions ?? []} onChange={(values) => setNamespaceGrant(namespace.id, values as ConfigNamespacePermission[])} /></section>)}
            {unavailableNamespaceGrants.map((grant) => <section className="model-grant" key={grant.config_namespace_id}><Typography.Text strong><code>{grant.config_namespace_id}</code> <Tag>已归档或不可见</Tag></Typography.Text><Checkbox.Group disabled={!canManage || selectedImmutable} options={configNamespacePermissionCodes.map((code) => ({ label: code, value: code, disabled: !grant.permissions.includes(code) }))} value={grant.permissions} onChange={(values) => setNamespaceGrant(grant.config_namespace_id, values as ConfigNamespacePermission[])} /></section>)}
            {!canViewNamespaces ? <Typography.Paragraph type="secondary">需要 configurations.view 才能加载 active 命名空间并新增权限；仍可清理角色已有的不可见命名空间权限。</Typography.Paragraph> : null}
            <Button onClick={saveNamespaces} disabled={!canManage || selectedImmutable}>保存配置权限</Button>
          </> : <Typography.Text type="secondary">{canView ? '选择角色查看权限。' : '当前账号可新建角色，但没有角色列表查看权限。'}</Typography.Text>}
        </section>
      </div>
    </DataState>
    <CreateRoleButton disabled={!canManage} onCreated={roles.reload} />
  </>
}

function CreateRoleButton({ disabled, onCreated }: { disabled: boolean; onCreated: () => void }) {
  const [open, setOpen] = useState(false)
  const [form] = Form.useForm()
  return <>
    <Button className="floating-action" type="primary" disabled={disabled} onClick={() => setOpen(true)}>新建角色</Button>
    <Modal title="新建无授权角色" open={open} onCancel={() => setOpen(false)} onOk={() => form.validateFields().then(api.createRole).then(() => { setOpen(false); form.resetFields(); onCreated() }).catch((error) => { message.error(apiErrorMessage(error, '新建角色失败')); throw error })}>
      <Form form={form} layout="vertical"><Form.Item name="key" label="稳定标识" rules={[{ required: true }, { pattern: /^[a-z][a-z0-9_]{0,63}$/ }]}><Input /></Form.Item><Form.Item name="display_name" label="显示名称" rules={[{ required: true }]}><Input /></Form.Item><Form.Item name="description" label="说明"><Input.TextArea /></Form.Item></Form>
    </Modal>
  </>
}
