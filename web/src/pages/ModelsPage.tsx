import { Button, Form, Input, Modal, Table, Tag, Typography, message } from 'antd'
import { useState } from 'react'
import { Link } from 'react-router-dom'

import { api, apiErrorMessage } from '../api/client'
import type { ContentModelSummary, Principal } from '../api/types'
import { hasModelPermission, hasSystemPermission } from '../auth/permissions'
import { DataState, PageHeader, useApiData } from '../components/Page'

export default function ModelsPage({ principal }: { principal: Principal }) {
  const models = useApiData(() => api.listModels())
  const [open, setOpen] = useState(false)
  const [form] = Form.useForm()
  const canCreate = hasSystemPermission(principal, 'models.create')
  return <><PageHeader eyebrow="内容结构" title="模型列表" description="模型定义稳定的数据边界，字段标识一经创建不可修改。" extra={<Button type="primary" disabled={!canCreate} onClick={() => setOpen(true)}>新建模型</Button>} /><DataState loading={models.loading} error={models.error} empty={!models.data?.items.length} retry={models.reload}><Table<ContentModelSummary> rowKey="id" dataSource={models.data?.items} pagination={false} scroll={{ x: 680 }} columns={[
    { title: '模型', render: (_, row) => <div><Link to={`/models/${row.id}`}><Typography.Text strong>{row.display_name}</Typography.Text></Link><br/><code>{row.key}</code></div> },
    { title: '说明', dataIndex: 'description', render: (value: string) => value || '无' },
    { title: '状态', dataIndex: 'status', render: (value: string) => <Tag color={value === 'active' ? 'green' : 'default'}>{value === 'active' ? '使用中' : '已归档'}</Tag> },
    { title: '内容', render: (_, row) => hasModelPermission(principal, row.id, 'content.view') ? <Link to={`/content/${row.id}`}>查看草稿</Link> : <Typography.Text type="secondary">无查看权限</Typography.Text> },
    { title: '更新于', dataIndex: 'updated_at', render: (value: string) => new Date(value).toLocaleString('zh-CN') },
  ]} /></DataState><Modal title="新建内容模型" open={open} onCancel={() => setOpen(false)} onOk={() => form.validateFields().then(api.createModel).then(() => { setOpen(false); form.resetFields(); models.reload() }).catch((error) => { message.error(apiErrorMessage(error, '新建内容模型失败')); throw error })}><Form form={form} layout="vertical"><Form.Item label="稳定标识" name="key" rules={[{ required: true }, { pattern: /^[a-z][a-z0-9_]{0,63}$/, message: '仅限小写字母、数字和下划线' }]}><Input /></Form.Item><Form.Item label="显示名称" name="display_name" rules={[{ required: true }]}><Input /></Form.Item><Form.Item label="说明" name="description"><Input.TextArea /></Form.Item></Form></Modal></>
}
