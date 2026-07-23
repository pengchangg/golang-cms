import { Button, Input, Select, Space, Table, Tag, Typography } from 'antd'
import { useState } from 'react'
import { Link, useParams } from 'react-router-dom'

import { api } from '../api/client'
import type { ContentEntrySummary, Principal, WorkflowStatus } from '../api/types'
import { hasModelPermission } from '../auth/permissions'
import { EntryFieldValue } from '../components/EntryFieldValue'
import { DataState, PageHeader, PendingApiNotice, useApiData } from '../components/Page'
import { TransferActions } from '../components/TransferActions'

const workflowLabels: Record<WorkflowStatus, string> = {
  draft: '草稿', pending_review: '待审核', rejected: '已驳回', published: '已发布', unpublished: '已下线',
}

export default function EntriesPage({ principal }: { principal: Principal }) {
  const { modelId = '' } = useParams()
  const [workflowStatus, setWorkflowStatus] = useState<WorkflowStatus | undefined>()
  const [filterField, setFilterField] = useState<string>()
  const [filterValue, setFilterValue] = useState('')
  const [relationField, setRelationField] = useState<string>()
  const [relationValue, setRelationValue] = useState('')
  const [sort, setSort] = useState<string>()
  const [cursors, setCursors] = useState<Array<string | undefined>>([undefined])
  const cursor = cursors.at(-1)
  const filter = filterField && filterValue ? JSON.stringify({ [filterField]: { eq: filterValue } }) : undefined
  const relation_filter = relationField && relationValue ? JSON.stringify({ [relationField]: { contains: relationValue } }) : undefined
  const entries = useApiData(() => api.listEntries(modelId, { workflow_status: workflowStatus, filter, relation_filter, sort, cursor, include_total: true }), [modelId, workflowStatus, filter, relation_filter, sort, cursor])
  const canCreate = hasModelPermission(principal, modelId, 'content.create')
  const canReview = hasModelPermission(principal, modelId, 'content.review')
  const canViewModel = (targetModelId: string) => hasModelPermission(principal, targetModelId, 'content.view')
  const fields = entries.data?.fields ?? []
  const scalarFields = fields.filter((field) => field.constraints.filterable && field.type !== 'single_relation' && field.type !== 'multi_relation')
  const relationFields = fields.filter((field) => field.type === 'single_relation' || field.type === 'multi_relation')
  const sortableFields = fields.filter((field) => field.constraints.sortable)
  const selectedFilter = scalarFields.find((field) => field.key === filterField)

  return <><PageHeader eyebrow={workflowStatus === 'pending_review' ? '审核队列' : '动态内容'} title={workflowStatus === 'pending_review' ? '待审核内容' : '内容列表'} description="按工作流状态组织内容，并以模型声明的字段做基础过滤和稳定排序。" extra={<Space wrap><TransferActions principal={principal} modelId={modelId} exportQuery={{ workflow_status: workflowStatus, filter, relation_filter, sort }} onImported={entries.reload} /><Link to={`/content/${modelId}/new`}><Button type="primary" disabled={!canCreate}>新建草稿</Button></Link></Space>} /><PendingApiNotice /><section className="content-filter-panel" aria-label="内容筛选与排序"><Space wrap>
    <Select aria-label="工作流状态" allowClear placeholder="全部工作流状态" value={workflowStatus} onChange={(value) => { setWorkflowStatus(value); setCursors([undefined]) }} options={Object.entries(workflowLabels).map(([value, label]) => ({ value, label }))} />
    {canReview ? <Button type={workflowStatus === 'pending_review' ? 'primary' : 'default'} onClick={() => { setWorkflowStatus('pending_review'); setCursors([undefined]) }}>待审核队列</Button> : null}
    <Select aria-label="过滤字段" allowClear placeholder="过滤字段" value={filterField} onChange={(value) => { setFilterField(value); setCursors([undefined]) }} options={scalarFields.map((field) => ({ value: field.key, label: field.display_name }))} />
    {selectedFilter?.constraints.enum_options?.length ? <Select aria-label="等于值" placeholder="等于值" value={filterValue || undefined} onChange={(value) => { setFilterValue(value); setCursors([undefined]) }} options={selectedFilter.constraints.enum_options.map((option) => ({ value: option.value, label: option.label }))} /> : <Input aria-label="等于值" placeholder="等于值" value={filterValue} onChange={(event) => { setFilterValue(event.target.value); setCursors([undefined]) }} disabled={!filterField} />}
    <Select aria-label="关联字段" allowClear placeholder="关联字段" value={relationField} onChange={(value) => { setRelationField(value); setCursors([undefined]) }} options={relationFields.map((field) => ({ value: field.key, label: field.display_name }))} />
    <Input aria-label="关联条目 ID" placeholder="包含条目 ID" value={relationValue} onChange={(event) => { setRelationValue(event.target.value); setCursors([undefined]) }} disabled={!relationField} />
    <Select aria-label="排序" allowClear placeholder="默认排序" value={sort} onChange={(value) => { setSort(value); setCursors([undefined]) }} options={sortableFields.flatMap((field) => [{ value: field.key, label: `${field.display_name}升序` }, { value: `-${field.key}`, label: `${field.display_name}降序` }])} />
  </Space></section>{entries.data?.total !== undefined ? <Typography.Text className="result-count" type="secondary">匹配 {entries.data.total}{entries.data.total_is_estimate ? '+' : ''} 条</Typography.Text> : null}<DataState loading={entries.loading} error={entries.error} empty={!entries.data?.items.length} retry={entries.reload}><Table<ContentEntrySummary> rowKey="id" dataSource={entries.data?.items} pagination={false} scroll={{ x: 'max-content' }} columns={[
    { title: '内容 ID', dataIndex: 'id', render: (value: string) => <Link to={`/content/${modelId}/${value}`}><code>{value}</code></Link> },
    ...fields.map((field) => ({ title: field.display_name, key: field.key, width: field.type === 'single_media' || field.type === 'multi_media' ? 280 : 180, render: (_: unknown, item: ContentEntrySummary) => <EntryFieldValue field={field} item={item} modelId={modelId} canViewModel={canViewModel} /> })),
    { title: '工作流', dataIndex: 'workflow_status', render: (value: WorkflowStatus) => <Tag color={value === 'pending_review' ? 'gold' : value === 'published' ? 'green' : value === 'rejected' ? 'red' : 'default'}>{workflowLabels[value]}</Tag> },
    { title: '更新于', dataIndex: 'updated_at', render: (value: string) => new Date(value).toLocaleString('zh-CN') },
  ]} /></DataState><Space className="pagination-actions"><Button disabled={cursors.length === 1} onClick={() => setCursors((values) => values.slice(0, -1))}>上一页</Button><Button disabled={!entries.data?.next_cursor} onClick={() => entries.data?.next_cursor && setCursors((values) => [...values, entries.data!.next_cursor!])}>下一页</Button></Space></>
}
