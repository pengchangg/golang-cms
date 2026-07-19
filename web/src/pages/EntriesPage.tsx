import { Button, Table, Tag } from 'antd'
import { Link, useParams } from 'react-router-dom'

import { api } from '../api/client'
import type { ContentEntrySummary, Principal } from '../api/types'
import { hasModelPermission } from '../auth/permissions'
import { DataState, PageHeader, PendingApiNotice, useApiData } from '../components/Page'

export default function EntriesPage({ principal }: { principal: Principal }) {
  const { modelId = '' } = useParams(); const entries = useApiData(() => api.listEntries(modelId), [modelId])
  const canCreate = hasModelPermission(principal, modelId, 'content.create')
  return <><PageHeader eyebrow="动态草稿" title="内容列表" description="每次有效保存都会形成不可变 Revision，这里默认只显示草稿。" extra={<Link to={`/content/${modelId}/new`}><Button type="primary" disabled={!canCreate}>新建草稿</Button></Link>} /><PendingApiNotice /><DataState loading={entries.loading} error={entries.error} empty={!entries.data?.items.length} retry={entries.reload}><Table<ContentEntrySummary> rowKey="id" dataSource={entries.data?.items} pagination={false} scroll={{ x: 650 }} columns={[{ title: '内容 ID', dataIndex: 'id', render: (value: string) => <Link to={`/content/${modelId}/${value}`}><code>{value}</code></Link> }, { title: '状态', dataIndex: 'status', render: (value: string) => <Tag>{value === 'draft' ? '草稿' : '已归档'}</Tag> }, { title: '当前版本', dataIndex: 'current_draft_revision_id', render: (value: string) => <code>{value}</code> }, { title: '创建人', dataIndex: 'created_by' }, { title: '更新于', dataIndex: 'updated_at', render: (value: string) => new Date(value).toLocaleString('zh-CN') }]} /></DataState></>
}
