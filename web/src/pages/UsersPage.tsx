import { Button, Input, Modal, Select, Space, Switch, Table, Tag, Typography, message } from 'antd'
import { useState } from 'react'

import { api, apiErrorMessage } from '../api/client'
import type { Principal, User, UserSummary } from '../api/types'
import { hasSystemPermission } from '../auth/permissions'
import { DataState, PageHeader, PendingApiNotice, useApiData } from '../components/Page'

export default function UsersPage({ principal }: { principal: Principal }) {
  const [query, setQuery] = useState('')
  const [status, setStatus] = useState<string>()
  const [editing, setEditing] = useState<User>()
  const users = useApiData(() => api.listUsers({ query, status }), [query, status])
  const canManage = hasSystemPermission(principal, 'users.manage')
  const canViewRoles = hasSystemPermission(principal, 'roles.view')
  const canManageRoles = hasSystemPermission(principal, 'roles.manage')
  const roles = useApiData(() => canViewRoles ? api.listRoles() : Promise.resolve({ items: [] }), [canViewRoles])
  async function toggle(user: UserSummary) {
    try {
      await api.setUserStatus(user.id, user.status === 'enabled' ? 'disabled' : 'enabled')
      users.reload()
    } catch (error) {
      message.error(apiErrorMessage(error, '更新用户状态失败'))
    }
  }
  async function saveRoles() {
    if (!editing) return
    try {
      await api.replaceUserRoles(editing.id, editing.role_ids)
      message.success('用户角色已原子替换')
      setEditing(undefined)
    } catch (error) {
      message.error(apiErrorMessage(error, '保存用户角色失败'))
      throw error
    }
  }
  return <><PageHeader eyebrow="身份目录" title="用户管理" description="查看企业身份、应急管理员状态与角色绑定，禁用会立即使现有会话失效。" /><PendingApiNotice /><Space className="filter-bar" wrap><Input.Search aria-label="搜索用户" placeholder="姓名或邮箱" allowClear onSearch={setQuery} /><Select aria-label="用户状态" placeholder="全部状态" allowClear value={status} onChange={setStatus} options={[{ value: 'enabled', label: '已启用' }, { value: 'disabled', label: '已禁用' }]} /></Space><DataState loading={users.loading || roles.loading} error={users.error ?? roles.error} empty={!users.data?.items.length} retry={() => { users.reload(); roles.reload() }}><Table<UserSummary> rowKey="id" dataSource={users.data?.items} pagination={false} scroll={{ x: 820 }} columns={[
    { title: '用户', render: (_, row) => <div><Typography.Text strong>{row.display_name}</Typography.Text><br/><Typography.Text type="secondary">{row.email ?? '无邮箱'}</Typography.Text></div> },
    { title: '身份', dataIndex: 'auth_methods', render: (value: string[]) => value.map((item) => <Tag key={item}>{item === 'oidc' ? 'SSO' : '本地'}</Tag>) },
    { title: '应急管理员', dataIndex: 'is_emergency_admin', render: (value: boolean) => value ? '是' : '否' },
    { title: '状态', render: (_, row) => <Switch checkedChildren="启用" unCheckedChildren="禁用" checked={row.status === 'enabled'} disabled={!canManage} onChange={() => toggle(row)} /> },
    { title: '角色', render: (_, row) => canViewRoles ? <Button type="link" disabled={!canManageRoles} onClick={() => api.getUser(row.id).then(setEditing).catch((error) => message.error(apiErrorMessage(error, '加载用户角色失败')))}>管理角色</Button> : <Typography.Text type="secondary">无查看权限</Typography.Text> },
    { title: '更新于', dataIndex: 'updated_at', render: (value: string) => new Date(value).toLocaleString('zh-CN') },
  ]} /></DataState><Modal title={`分配角色 · ${editing?.display_name ?? ''}`} open={Boolean(editing)} onCancel={() => setEditing(undefined)} onOk={saveRoles}><Select mode="multiple" style={{ width: '100%' }} aria-label="用户角色" value={editing?.role_ids} options={roles.data?.items.map((role) => ({ value: role.id, label: role.display_name }))} onChange={(role_ids) => editing && setEditing({ ...editing, role_ids })} /></Modal></>
}
