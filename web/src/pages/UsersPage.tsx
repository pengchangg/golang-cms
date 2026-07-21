import { Button, Form, Input, Modal, Select, Space, Switch, Table, Tag, Typography, message } from 'antd'
import { useState } from 'react'

import { api, apiErrorMessage } from '../api/client'
import type { AuthMethod, Principal, User, UserSummary } from '../api/types'
import { hasSystemPermission } from '../auth/permissions'
import { DataState, PageHeader, PendingApiNotice, useApiData } from '../components/Page'

const mainlandPhonePattern = /^1[3-9]\d{9}$/
const authMethodLabels: Record<AuthMethod, string> = { sms: '短信', local: '本地' }

interface CreateUserValues {
  display_name: string
  phone: string
  role_ids: string[]
}

export default function UsersPage({ principal }: { principal: Principal }) {
  const [query, setQuery] = useState('')
  const [status, setStatus] = useState<string>()
  const [editingRoles, setEditingRoles] = useState<User>()
  const [editingPhone, setEditingPhone] = useState<User>()
  const [creating, setCreating] = useState(false)
  const [saving, setSaving] = useState(false)
  const [createForm] = Form.useForm<CreateUserValues>()
  const [phoneForm] = Form.useForm<{ phone: string }>()
  const users = useApiData(() => api.listUsers({ query, status }), [query, status])
  const canManage = hasSystemPermission(principal, 'users.manage')
  const canViewRoles = hasSystemPermission(principal, 'roles.view')
  const canManageRoles = hasSystemPermission(principal, 'roles.manage')
  const canCreate = canManage && canManageRoles
  const roles = useApiData(() => canViewRoles || canCreate ? api.listRoles() : Promise.resolve({ items: [] }), [canViewRoles, canCreate])

  async function toggle(user: UserSummary) {
    try {
      await api.setUserStatus(user.id, user.status === 'enabled' ? 'disabled' : 'enabled')
      users.reload(true)
    } catch (error) {
      message.error(apiErrorMessage(error, '更新用户状态失败'))
    }
  }

  async function saveRoles() {
    if (!editingRoles) return
    try {
      await api.replaceUserRoles(editingRoles.id, editingRoles.role_ids)
      message.success('用户角色已更新')
      setEditingRoles(undefined)
      users.reload(true)
    } catch (error) {
      message.error(apiErrorMessage(error, '保存用户角色失败'))
      throw error
    }
  }

  async function createUser() {
    let values: CreateUserValues
    try {
      values = await createForm.validateFields()
    } catch {
      return
    }
    setSaving(true)
    try {
      await api.createUser(values)
      message.success('手机号账户已创建')
      setCreating(false)
      createForm.resetFields()
      users.reload()
    } catch (error) {
      message.error(apiErrorMessage(error, '创建手机号账户失败'))
    } finally {
      setSaving(false)
    }
  }

  async function updatePhone() {
    if (!editingPhone) return
    let phone: string
    try {
      phone = (await phoneForm.validateFields()).phone
    } catch {
      return
    }
    setSaving(true)
    try {
      await api.updateUserPhone(editingPhone.id, phone)
      message.success('手机号已更新')
      setEditingPhone(undefined)
      phoneForm.resetFields()
      users.reload()
    } catch (error) {
      message.error(apiErrorMessage(error, '更新手机号失败'))
    } finally {
      setSaving(false)
    }
  }

  async function openPhoneEditor(user: UserSummary) {
    try {
      const detail = await api.getUser(user.id)
      phoneForm.resetFields()
      setEditingPhone(detail)
    } catch (error) {
      message.error(apiErrorMessage(error, '加载用户详情失败'))
    }
  }

  return <>
    <PageHeader
      eyebrow="身份目录"
      title="用户管理"
      description="管理手机号账户、应急管理员状态与角色绑定，禁用会立即使现有会话失效。"
      extra={canCreate ? <Button type="primary" onClick={() => setCreating(true)}>创建手机号账户</Button> : null}
    />
    <PendingApiNotice />
    <Space className="filter-bar" wrap>
      <Input.Search aria-label="搜索用户" placeholder="姓名或手机号" allowClear onSearch={setQuery} />
      <Select aria-label="用户状态" placeholder="全部状态" allowClear value={status} onChange={setStatus} options={[{ value: 'enabled', label: '已启用' }, { value: 'disabled', label: '已禁用' }]} />
    </Space>
    <DataState loading={users.loading || roles.loading} error={users.error ?? roles.error} empty={!users.data?.items.length} retry={() => { users.reload(); roles.reload() }}>
      <Table<UserSummary> rowKey="id" dataSource={users.data?.items} pagination={false} scroll={{ x: 920 }} columns={[
        { title: '用户', render: (_, row) => <div><Typography.Text strong>{row.display_name}</Typography.Text><br/><Typography.Text type="secondary">{row.phone_masked ?? '未绑定手机号'}</Typography.Text></div> },
        { title: '身份', dataIndex: 'auth_methods', render: (value: AuthMethod[]) => value.map((item) => <Tag key={item}>{authMethodLabels[item]}</Tag>) },
        { title: '应急管理员', dataIndex: 'is_emergency_admin', render: (value: boolean) => value ? '是' : '否' },
        { title: '状态', render: (_, row) => <Switch checkedChildren="启用" unCheckedChildren="禁用" checked={row.status === 'enabled'} disabled={!canManage} onChange={() => void toggle(row)} /> },
        { title: '账户', render: (_, row) => <Button type="link" disabled={!canManage || !canViewRoles || row.is_emergency_admin} onClick={() => void openPhoneEditor(row)}>编辑手机号</Button> },
        { title: '角色', render: (_, row) => canViewRoles ? <Button type="link" disabled={!canManageRoles} onClick={() => api.getUser(row.id).then(setEditingRoles).catch((error) => message.error(apiErrorMessage(error, '加载用户角色失败')))}>管理角色</Button> : <Typography.Text type="secondary">无查看权限</Typography.Text> },
        { title: '更新于', dataIndex: 'updated_at', render: (value: string) => new Date(value).toLocaleString('zh-CN') },
      ]} />
    </DataState>

    <Modal title="创建手机号账户" open={creating} confirmLoading={saving} onCancel={() => setCreating(false)} onOk={() => void createUser()} okText="创建">
      <Form<CreateUserValues> form={createForm} layout="vertical" requiredMark={false}>
        <Form.Item label="显示名称" name="display_name" rules={[{ required: true, message: '请输入显示名称' }]}><Input maxLength={128} /></Form.Item>
        <Form.Item label="手机号" name="phone" rules={[{ required: true, message: '请输入手机号' }, { pattern: mainlandPhonePattern, message: '请输入有效的大陆手机号' }]}><Input prefix="+86" inputMode="tel" maxLength={11} /></Form.Item>
        <Form.Item label="角色" name="role_ids" initialValue={[]}><Select mode="multiple" options={roles.data?.items.map((role) => ({ value: role.id, label: role.display_name }))} /></Form.Item>
      </Form>
    </Modal>

    <Modal title={`编辑手机号 · ${editingPhone?.display_name ?? ''}`} open={Boolean(editingPhone)} confirmLoading={saving} onCancel={() => setEditingPhone(undefined)} onOk={() => void updatePhone()} okText="保存">
      <Form form={phoneForm} layout="vertical" requiredMark={false}>
        <Typography.Paragraph type="secondary">当前手机号：{editingPhone?.phone ?? '未绑定'}</Typography.Paragraph>
        <Form.Item label="新手机号" name="phone" rules={[{ required: true, message: '请输入手机号' }, { pattern: mainlandPhonePattern, message: '请输入有效的大陆手机号' }]}><Input prefix="+86" inputMode="tel" maxLength={11} /></Form.Item>
      </Form>
    </Modal>

    <Modal title={`分配角色 · ${editingRoles?.display_name ?? ''}`} open={Boolean(editingRoles)} onCancel={() => setEditingRoles(undefined)} onOk={saveRoles}>
      <Select mode="multiple" style={{ width: '100%' }} aria-label="用户角色" value={editingRoles?.role_ids} options={roles.data?.items.map((role) => ({ value: role.id, label: role.display_name }))} onChange={(role_ids) => editingRoles && setEditingRoles({ ...editingRoles, role_ids })} />
    </Modal>
  </>
}
