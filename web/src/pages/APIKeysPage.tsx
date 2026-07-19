import { CopyOutlined, KeyOutlined } from '@ant-design/icons'
import { Alert, Button, DatePicker, Form, Input, Modal, Select, Space, Table, Tag, Typography, message } from 'antd'
import { useState } from 'react'

import { api } from '../api/client'
import type { APIKey, APIKeySecret, APIKeyStatus, CreateAPIKeyRequest, Principal } from '../api/types'
import { hasSystemPermission } from '../auth/permissions'
import { DataState, PageHeader, PendingApiNotice, useApiData } from '../components/Page'

const statusLabels: Record<APIKeyStatus, string> = { active: '有效', expired: '已过期', revoked: '已撤销' }

export default function APIKeysPage({ principal }: { principal: Principal }) {
  const [status, setStatus] = useState<APIKeyStatus | undefined>()
  const [cursors, setCursors] = useState<Array<string | undefined>>([undefined])
  const cursor = cursors.at(-1)
  const keys = useApiData(() => api.listAPIKeys(status, cursor), [status, cursor])
  const models = useApiData(() => api.listModels('active'))
  const [createOpen, setCreateOpen] = useState(false)
  const [secret, setSecret] = useState<APIKeySecret>()
  const [form] = Form.useForm()
  const canCreate = hasSystemPermission(principal, 'api_keys.create')
  const canRevoke = hasSystemPermission(principal, 'api_keys.revoke')

  function showSecret(value: APIKeySecret) { setCreateOpen(false); form.resetFields(); setSecret(value); keys.reload() }
  async function create(values: { name: string; model_ids: string[]; expires_at?: { toISOString(): string } }) {
    const body: CreateAPIKeyRequest = { name: values.name.trim(), model_ids: values.model_ids, expires_at: values.expires_at?.toISOString() ?? null }
    showSecret(await api.createAPIKey(body))
  }
  async function revoke(row: APIKey) {
    Modal.confirm({ title: `撤销 ${row.name}？`, content: '撤销立即生效且不可恢复。', okText: '确认撤销', okButtonProps: { danger: true }, onOk: async () => { await api.revokeAPIKey(row.id); message.success('API Key 已撤销'); keys.reload() } })
  }
  async function rotate(row: APIKey) {
    Modal.confirm({ title: `轮换 ${row.name}？`, content: '新 Key 继承当前名称、模型范围和过期时间，旧 Key 会立即失效。', okText: '确认轮换', onOk: async () => showSecret(await api.rotateAPIKey(row.id)) })
  }

  return <><PageHeader eyebrow="客户端访问" title="API Keys" description="每个 Key 明确限定可读取的模型。完整密钥不持久化，只在创建或轮换后展示一次。" extra={<Button type="primary" icon={<KeyOutlined />} disabled={!canCreate} onClick={() => setCreateOpen(true)}>创建 API Key</Button>} /><PendingApiNotice /><Select className="api-key-filter" aria-label="API Key 状态" allowClear placeholder="全部状态" value={status} onChange={(value) => { setStatus(value); setCursors([undefined]) }} options={Object.entries(statusLabels).map(([value, label]) => ({ value, label }))} /><DataState loading={keys.loading} error={keys.error} empty={!keys.data?.items.length} retry={keys.reload}><Table<APIKey> rowKey="id" dataSource={keys.data?.items} pagination={false} scroll={{ x: 900 }} columns={[
    { title: '名称与前缀', render: (_, row) => <div><Typography.Text strong>{row.name}</Typography.Text><br/><code>{row.prefix}</code></div> },
    { title: '模型范围', dataIndex: 'model_ids', render: (ids: string[]) => <Space wrap>{ids.map((id) => <Tag key={id}>{models.data?.items.find((model) => model.id === id)?.display_name ?? id}</Tag>)}</Space> },
    { title: '状态', dataIndex: 'status', render: (value: APIKeyStatus) => <Tag color={value === 'active' ? 'green' : value === 'expired' ? 'gold' : 'default'}>{statusLabels[value]}</Tag> },
    { title: '最近使用', dataIndex: 'last_used_at', render: (value: string | null) => value ? new Date(value).toLocaleString('zh-CN') : '从未使用' },
    { title: '过期时间', dataIndex: 'expires_at', render: (value: string | null) => value ? new Date(value).toLocaleString('zh-CN') : '永不过期' },
    { title: '操作', fixed: 'right', render: (_, row) => <Space><Button size="small" disabled={!canCreate || row.status !== 'active'} onClick={() => rotate(row)}>轮换</Button><Button size="small" danger disabled={!canRevoke || row.status !== 'active'} onClick={() => revoke(row)}>撤销</Button></Space> },
  ]} /></DataState><Space className="pagination-actions"><Button disabled={cursors.length === 1} onClick={() => setCursors((values) => values.slice(0, -1))}>上一页</Button><Button disabled={!keys.data?.next_cursor} onClick={() => keys.data?.next_cursor && setCursors((values) => [...values, keys.data!.next_cursor!])}>下一页</Button></Space><Modal title="创建 API Key" open={createOpen} onCancel={() => setCreateOpen(false)} onOk={() => form.submit()} okText="创建"><Form form={form} layout="vertical" onFinish={create}><Form.Item label="名称" name="name" rules={[{ required: true }, { max: 120 }]}><Input maxLength={120} /></Form.Item><Form.Item label="模型范围" name="model_ids" rules={[{ required: true, message: '至少选择一个模型' }]}><Select mode="multiple" options={models.data?.items.map((model) => ({ value: model.id, label: model.display_name }))} /></Form.Item><Form.Item label="过期时间" name="expires_at"><DatePicker showTime disabledDate={(date) => date.valueOf() <= Date.now()} /></Form.Item></Form></Modal><Modal className="secret-modal" title="仅此一次：保存完整 API Key" open={Boolean(secret)} closable={false} mask={{ closable: false }} keyboard={false} footer={<Button type="primary" onClick={() => setSecret(undefined)}>我已安全保存</Button>}><Alert type="warning" showIcon title="关闭后无法再次查看" description="此响应按 no-store 获取。不要将密钥粘贴到工单、日志或聊天记录。" /><Typography.Paragraph className="secret-value" copyable={{ icon: <CopyOutlined />, tooltips: ['复制完整 Key', '已复制'] }}><code>{secret?.key}</code></Typography.Paragraph><Typography.Text type="secondary">模型范围：{secret?.model_ids.join('、')}</Typography.Text></Modal></>
}
