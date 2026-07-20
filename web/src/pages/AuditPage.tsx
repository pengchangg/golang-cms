import { Descriptions, Select, Space, Table, Tag, Typography } from 'antd'
import { useState } from 'react'
import { Link } from 'react-router-dom'

import { api } from '../api/client'
import type { AuditEvent, Principal } from '../api/types'
import { auditActionLabels, auditResourceLabels, auditResourcePath } from '../auditMeta'
import { DataState, PageHeader, PendingApiNotice, useApiData } from '../components/Page'

export default function AuditPage({ principal }: { principal: Principal }) {
  const [action, setAction] = useState(''); const [result, setResult] = useState<string>(); const [selected, setSelected] = useState<AuditEvent>()
  const events = useApiData(() => api.listAuditEvents({ action, result }), [action, result])
  const actionOptions = Object.entries(auditActionLabels).map(([value, label]) => ({ value, label: `${label} · ${value}` }))
  const actor = (row: AuditEvent) => row.actor_type === 'system' ? <strong>系统</strong> : <div className="audit-primary"><strong>{row.actor_display_name ?? '未知用户'}</strong><code>{row.actor_id}</code></div>
  const resource = (row: AuditEvent) => {
    const path = auditResourcePath(row, principal)
    const content = <div className="audit-primary"><strong>{auditResourceLabels[row.resource_type] ?? row.resource_type}</strong>{row.resource_id ? <code>{row.resource_id}</code> : null}</div>
    return path ? <Link to={path} onClick={(event) => event.stopPropagation()}>{content}</Link> : content
  }
  return <><PageHeader eyebrow="只追加记录" title="基础审计日志" description="按发生时间倒序查询成功与失败事件，查询本身不会递归产生审计事件。" /><PendingApiNotice /><Space className="filter-bar" wrap><Select className="audit-action-filter" aria-label="筛选动作" showSearch allowClear placeholder="按中文动作筛选" value={action || undefined} onChange={(value) => setAction(value ?? '')} options={actionOptions} /><Select aria-label="筛选结果" placeholder="全部结果" allowClear value={result} onChange={setResult} options={[{ value: 'success', label: '成功' }, { value: 'failure', label: '失败' }]} /></Space><DataState loading={events.loading} error={events.error} empty={!events.data?.items.length} retry={events.reload}><Table<AuditEvent> rowKey="id" dataSource={events.data?.items} pagination={false} onRow={(row) => ({ onClick: () => setSelected(row) })} scroll={{ x: 980 }} columns={[{ title: '发生时间', dataIndex: 'occurred_at', render: (value: string) => new Date(value).toLocaleString('zh-CN') }, { title: '动作', dataIndex: 'action', render: (value: string) => <div className="audit-primary"><strong>{auditActionLabels[value] ?? value}</strong><code>{value}</code></div> }, { title: '操作者', render: (_, row) => actor(row) }, { title: '资源', render: (_, row) => resource(row) }, { title: '结果', dataIndex: 'result', render: (value: string) => <Tag color={value === 'success' ? 'green' : 'red'}>{value === 'success' ? '成功' : '失败'}</Tag> }]} /></DataState>{selected ? <Descriptions className="audit-detail" bordered column={1} title="事件详情" items={[{ key: 'actor', label: '操作者', children: actor(selected) }, { key: 'action', label: '动作', children: <>{auditActionLabels[selected.action] ?? selected.action} <code>{selected.action}</code></> }, { key: 'resource', label: '资源', children: resource(selected) }, { key: 'request', label: '请求 ID', children: <code>{selected.request_id}</code> }, { key: 'ip', label: 'IP', children: selected.ip }, { key: 'failure', label: '失败码', children: selected.failure_code ? <code>{selected.failure_code}</code> : '无' }, { key: 'changes', label: '变更摘要', children: <Typography.Text><pre>{JSON.stringify(selected.changes, null, 2)}</pre></Typography.Text> }]} /> : null}</>
}
