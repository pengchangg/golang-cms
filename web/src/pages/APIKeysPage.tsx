import { CopyOutlined, KeyOutlined } from '@ant-design/icons'
import { Alert, Button, DatePicker, Form, Input, Modal, Select, Space, Table, Tag, Typography, message } from 'antd'
import { useState } from 'react'

import { api, apiErrorMessage } from '../api/client'
import type { APIKey, APIKeySecret, APIKeyStatus, CreateAPIKeyRequest, Principal } from '../api/types'
import { hasSystemPermission } from '../auth/permissions'
import { DataState, PageHeader, PendingApiNotice, useApiData } from '../components/Page'

const statusLabels: Record<APIKeyStatus, string> = { active: '有效', expired: '已过期', revoked: '已撤销' }

export default function APIKeysPage({ principal }: { principal: Principal }) {
  const [status, setStatus] = useState<APIKeyStatus>()
  const [cursors, setCursors] = useState<Array<string | undefined>>([undefined])
  const [createOpen, setCreateOpen] = useState(false)
  const [secret, setSecret] = useState<APIKeySecret>()
  const [creating, setCreating] = useState(false)
  const [idAction, setIDAction] = useState<'rotate' | 'revoke'>()
  const [actionID, setActionID] = useState('')
  const [form] = Form.useForm()
  const cursor = cursors.at(-1)
  const canView = hasSystemPermission(principal, 'api_keys.view')
  const canCreate = hasSystemPermission(principal, 'api_keys.create')
  const canRevoke = hasSystemPermission(principal, 'api_keys.revoke')
  const canViewModels = hasSystemPermission(principal, 'models.view')
  const readableModelIDs = principal.model_permissions.filter((grant) => grant.permissions.includes('content.view')).map((grant) => grant.model_id)
  const keys = useApiData(() => canView ? api.listAPIKeys(status, cursor) : Promise.resolve({ items: [], next_cursor: null }), [canView, status, cursor])
  const models = useApiData(() => canViewModels ? api.listModels('active') : Promise.resolve({ items: [] }), [canViewModels])

  function showSecret(value: APIKeySecret) {
    setCreateOpen(false)
    setIDAction(undefined)
    setActionID('')
    form.resetFields()
    setSecret(value)
    keys.reload()
  }

  async function create(values: { name: string; model_ids: string[]; expires_at?: { toISOString(): string } }) {
    const body: CreateAPIKeyRequest = { name: values.name.trim(), model_ids: values.model_ids, expires_at: values.expires_at?.toISOString() ?? null }
    setCreating(true)
    try { showSecret(await api.createAPIKey(body)) }
    catch (error) { message.error(apiErrorMessage(error, '创建 API Key 失败')) }
    finally { setCreating(false) }
  }

  async function revoke(row: APIKey) {
    Modal.confirm({
      title: `撤销 ${row.name}？`,
      content: '撤销立即生效且不可恢复。',
      okText: '确认撤销',
      okButtonProps: { danger: true },
      onOk: async () => {
        try { await api.revokeAPIKey(row.id); message.success('API Key 已撤销'); keys.reload() }
        catch (error) { message.error(apiErrorMessage(error, '撤销 API Key 失败')); throw error }
      },
    })
  }

  async function rotate(row: APIKey) {
    Modal.confirm({
      title: `轮换 ${row.name}？`,
      content: '新 Key 继承当前名称、模型范围和过期时间，旧 Key 会立即失效。',
      okText: '确认轮换',
      onOk: async () => {
        try { showSecret(await api.rotateAPIKey(row.id)) }
        catch (error) { message.error(apiErrorMessage(error, '轮换 API Key 失败')); throw error }
      },
    })
  }

  async function runIDAction() {
    const id = actionID.trim()
    if (!id || !idAction) return
    setCreating(true)
    try {
      if (idAction === 'rotate') showSecret(await api.rotateAPIKey(id))
      else { await api.revokeAPIKey(id); message.success('API Key 已撤销'); setIDAction(undefined); setActionID('') }
    } catch (error) {
      message.error(apiErrorMessage(error, idAction === 'rotate' ? '轮换 API Key 失败' : '撤销 API Key 失败'))
    } finally { setCreating(false) }
  }

  return <>
    <PageHeader
      eyebrow="客户端访问"
      title="API Keys"
      description="每个 Key 明确限定可读取的模型。完整密钥不持久化，只在创建或轮换后展示一次。"
      extra={<Space>
        <Button type="primary" icon={<KeyOutlined />} disabled={!canCreate || readableModelIDs.length === 0} onClick={() => setCreateOpen(true)}>创建 API Key</Button>
        {!canView && canCreate ? <Button onClick={() => setIDAction('rotate')}>按 ID 轮换</Button> : null}
        {!canView && canRevoke ? <Button danger onClick={() => setIDAction('revoke')}>按 ID 撤销</Button> : null}
      </Space>}
    />
    <PendingApiNotice />
    <Select className="api-key-filter" aria-label="API Key 状态" allowClear placeholder="全部状态" value={status} disabled={!canView} onChange={(value) => { keys.invalidate(); setStatus(value); setCursors([undefined]) }} options={Object.entries(statusLabels).map(([value, label]) => ({ value, label }))} />
    <DataState loading={keys.loading} error={keys.error} empty={canView && !keys.data?.items.length} retry={keys.reload}>
      <Table<APIKey> rowKey="id" dataSource={keys.data?.items} pagination={false} scroll={{ x: 900 }} columns={[
        { title: '名称与前缀', render: (_, row) => <div><Typography.Text strong>{row.name}</Typography.Text><br/><code>{row.prefix}</code></div> },
        { title: '模型范围', dataIndex: 'model_ids', render: (ids: string[]) => <Space wrap>{ids.map((id) => <Tag key={id}>{models.data?.items.find((model) => model.id === id)?.display_name ?? id}</Tag>)}</Space> },
        { title: '状态', dataIndex: 'status', render: (value: APIKeyStatus) => <Tag color={value === 'active' ? 'green' : value === 'expired' ? 'gold' : 'default'}>{statusLabels[value]}</Tag> },
        { title: '最近使用', dataIndex: 'last_used_at', render: (value: string | null) => value ? new Date(value).toLocaleString('zh-CN') : '从未使用' },
        { title: '过期时间', dataIndex: 'expires_at', render: (value: string | null) => value ? new Date(value).toLocaleString('zh-CN') : '永不过期' },
        { title: '操作', fixed: 'right', render: (_, row) => <Space><Button size="small" disabled={!canCreate || row.status !== 'active'} onClick={() => rotate(row)}>轮换</Button><Button size="small" danger disabled={!canRevoke || row.status !== 'active'} onClick={() => revoke(row)}>撤销</Button></Space> },
      ]} />
    </DataState>
    <Space className="pagination-actions">
      <Button disabled={cursors.length === 1} onClick={() => setCursors((values) => values.slice(0, -1))}>上一页</Button>
      <Button disabled={!keys.data?.next_cursor} onClick={() => { const next = keys.data?.next_cursor; if (next) setCursors((values) => values.at(-1) === next ? values : [...values, next]) }}>下一页</Button>
    </Space>
    <Modal title="创建 API Key" open={createOpen} onCancel={() => !creating && setCreateOpen(false)} onOk={() => form.submit()} okText="创建" okButtonProps={{ loading: creating }}>
      <Form form={form} layout="vertical" onFinish={create}>
        <Form.Item label="名称" name="name" rules={[{ required: true }, { max: 120 }]}><Input maxLength={120} /></Form.Item>
        <Form.Item label="模型范围" name="model_ids" rules={[{ required: true, message: '至少选择一个模型' }]}><Select mode="multiple" options={readableModelIDs.map((id) => ({ value: id, label: models.data?.items.find((model) => model.id === id)?.display_name ?? id }))} placeholder={readableModelIDs.length ? '选择当前可读取的模型' : '没有可签发的模型'} /></Form.Item>
        <Form.Item label="过期时间" name="expires_at"><DatePicker showTime disabledDate={(date) => date.valueOf() <= Date.now()} /></Form.Item>
      </Form>
    </Modal>
    <Modal title={idAction === 'rotate' ? '按 ID 轮换 API Key' : '按 ID 撤销 API Key'} open={Boolean(idAction)} onCancel={() => !creating && setIDAction(undefined)} onOk={runIDAction} okText={idAction === 'rotate' ? '轮换' : '撤销'} okButtonProps={{ loading: creating, danger: idAction === 'revoke', disabled: !actionID.trim() }}>
      <Typography.Paragraph type="secondary">没有列表查看权限时，请输入已知的 API Key ID。</Typography.Paragraph>
      <Input aria-label="API Key ID" value={actionID} onChange={(event) => setActionID(event.target.value)} placeholder="key_..." />
    </Modal>
    <Modal className="secret-modal" title="仅此一次：保存完整 API Key" open={Boolean(secret)} closable={false} mask={{ closable: false }} keyboard={false} footer={<Button type="primary" onClick={() => setSecret(undefined)}>我已安全保存</Button>}>
      <Alert type="warning" showIcon title="关闭后无法再次查看" description="此响应按 no-store 获取。不要将密钥粘贴到工单、日志或聊天记录。" />
      <Typography.Paragraph className="secret-value" copyable={{ icon: <CopyOutlined />, tooltips: ['复制完整 Key', '已复制'] }}><code>{secret?.key}</code></Typography.Paragraph>
      <Typography.Text type="secondary">模型范围：{secret?.model_ids.join('、')}</Typography.Text>
    </Modal>
  </>
}
