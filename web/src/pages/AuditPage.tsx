import { Descriptions, Input, Select, Space, Table, Tag, Typography } from 'antd'
import { useState } from 'react'

import { api } from '../api/client'
import type { AuditEvent } from '../api/types'
import { DataState, PageHeader, PendingApiNotice, useApiData } from '../components/Page'

export default function AuditPage() {
  const [action, setAction] = useState(''); const [result, setResult] = useState<string>(); const [selected, setSelected] = useState<AuditEvent>()
  const events = useApiData(() => api.listAuditEvents({ action, result }), [action, result])
  return <><PageHeader eyebrow="只追加记录" title="基础审计日志" description="按发生时间倒序查询成功与失败事件，查询本身不会递归产生审计事件。" /><PendingApiNotice /><Space className="filter-bar" wrap><Input.Search aria-label="筛选动作" placeholder="稳定 action 代码" onSearch={setAction} /><Select aria-label="筛选结果" placeholder="全部结果" allowClear value={result} onChange={setResult} options={[{ value: 'success', label: '成功' }, { value: 'failure', label: '失败' }]} /></Space><DataState loading={events.loading} error={events.error} empty={!events.data?.items.length} retry={events.reload}><Table<AuditEvent> rowKey="id" dataSource={events.data?.items} pagination={false} onRow={(row) => ({ onClick: () => setSelected(row) })} scroll={{ x: 760 }} columns={[{ title: '发生时间', dataIndex: 'occurred_at', render: (value: string) => new Date(value).toLocaleString('zh-CN') }, { title: '动作', dataIndex: 'action', render: (value: string) => <code>{value}</code> }, { title: '操作者', render: (_, row) => row.actor_id ?? row.actor_type }, { title: '资源', render: (_, row) => `${row.resource_type} · ${row.resource_id ?? '-'}` }, { title: '结果', dataIndex: 'result', render: (value: string) => <Tag color={value === 'success' ? 'green' : 'red'}>{value === 'success' ? '成功' : '失败'}</Tag> }]} /></DataState>{selected ? <Descriptions className="audit-detail" bordered column={1} title="事件详情" items={[{ key: 'request', label: '请求 ID', children: <code>{selected.request_id}</code> }, { key: 'ip', label: 'IP', children: selected.ip }, { key: 'changes', label: '变更摘要', children: <Typography.Text><pre>{JSON.stringify(selected.changes, null, 2)}</pre></Typography.Text> }]} /> : null}</>
}
