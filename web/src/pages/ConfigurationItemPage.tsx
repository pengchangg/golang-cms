import { Alert, Button, Descriptions, Input, Modal, Space, Switch, Tag, Timeline, Typography, message } from 'antd'
import { useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'

import { ApiError, api, apiErrorMessage } from '../api/client'
import type { ConfigurationItemValue, ConfigurationValueType, ConfigurationWorkflowEvent, Principal, WorkflowStatus } from '../api/types'
import { hasConfigNamespacePermission, hasModelPermission, hasSystemPermission } from '../auth/permissions'
import { AssetPicker } from '../components/AssetPicker'
import { DataState, PageHeader, useApiData } from '../components/Page'
import { RelationPicker } from '../components/RelationPicker'

const statusLabels: Record<WorkflowStatus, string> = { draft: '草稿', pending_review: '待审核', rejected: '已驳回', published: '已发布', unpublished: '已下线' }
const eventLabels: Record<ConfigurationWorkflowEvent['type'], string> = { submitted: '提交审核', approved: '审核通过并发布', rejected: '驳回', unpublished: '下线' }

export default function ConfigurationItemPage({ principal }: { principal: Principal }) {
  const { namespaceId = '', itemId = '' } = useParams()
  const navigate = useNavigate()
  const itemValue = useApiData(() => api.getConfigurationItemValue(namespaceId, itemId), [namespaceId, itemId])
  const [revisionCursors, setRevisionCursors] = useState<Array<string | undefined>>([undefined])
  const [eventCursors, setEventCursors] = useState<Array<string | undefined>>([undefined])
  const revisions = useApiData(() => api.listConfigurationRevisions(namespaceId, itemId, revisionCursors.at(-1)), [namespaceId, itemId, revisionCursors.at(-1)])
  const events = useApiData(() => api.listConfigurationWorkflowEvents(namespaceId, itemId, eventCursors.at(-1)), [namespaceId, itemId, eventCursors.at(-1)])
  const [draft, setDraft] = useState<unknown>()
  const [jsonError, setJSONError] = useState('')
  const [saving, setSaving] = useState(false)
  const [acting, setActing] = useState(false)
  const [reasonOpen, setReasonOpen] = useState(false)
  const [reason, setReason] = useState('')
  const data = itemValue.data
  const revision = data?.current_draft_revision
  const hasDraft = Boolean(revision?.id)
  const value = draft !== undefined ? draft : revision?.value ?? null
  const jsonText = data?.item.value_type === 'json' ? typeof draft === 'string' ? draft : JSON.stringify(revision?.value ?? null, null, 2) : ''
  const status = revision?.workflow_status
  const canCreate = hasConfigNamespacePermission(principal, namespaceId, 'config.create')
  const canUpdate = hasConfigNamespacePermission(principal, namespaceId, 'config.update')
  const canSubmit = hasConfigNamespacePermission(principal, namespaceId, 'config.submit')
  const canReview = hasConfigNamespacePermission(principal, namespaceId, 'config.review')
  const canPublish = hasConfigNamespacePermission(principal, namespaceId, 'config.publish')
  const canUnpublish = hasConfigNamespacePermission(principal, namespaceId, 'config.unpublish')
  const canEdit = data?.item.status === 'active' && (!hasDraft ? canCreate : canUpdate) && status !== 'pending_review'
  const dirty = draft !== undefined

  function apply(result: ConfigurationItemValue, success: string) {
    itemValue.setData(result); setDraft(undefined); setJSONError('')
    if (revisionCursors.length === 1) revisions.reload(); else setRevisionCursors([undefined])
    if (eventCursors.length === 1) events.reload(); else setEventCursors([undefined])
    message.success(success)
  }
  async function action(run: () => Promise<ConfigurationItemValue>, success: string) {
    setActing(true)
    try { apply(await run(), success); return true }
    catch (error) {
      message.error(error instanceof ApiError && ['workflow_revision_conflict', 'invalid_workflow_transition'].includes(error.code) ? '版本状态已变化，请重新载入' : apiErrorMessage(error, '工作流操作失败'))
      return false
    } finally { setActing(false) }
  }
  async function save() {
    if (!data) return
    let next = value
    if (data.item.value_type === 'json') {
      try { next = JSON.parse(jsonText) }
      catch { setJSONError('请输入合法 JSON'); return }
    }
    setSaving(true)
    try {
      const result = hasDraft ? await api.updateConfigurationDraft(namespaceId, itemId, revision!.id, next) : await api.createConfigurationDraft(namespaceId, itemId, next)
      apply(result, '配置草稿已保存为新 Revision')
    } catch (error) { message.error(apiErrorMessage(error, '保存配置草稿失败')) }
    finally { setSaving(false) }
  }

  const revisionId = revision?.id ?? ''
  const actions = data && hasDraft ? <Space wrap className="workflow-actions">
    {status === 'draft' && canSubmit ? <Button type="primary" disabled={dirty || acting} onClick={() => action(() => api.submitConfigurationItem(namespaceId, itemId, revisionId), '已提交审核')}>提交审核</Button> : null}
    {status === 'pending_review' && canReview ? <Button danger disabled={acting} onClick={() => setReasonOpen(true)}>驳回</Button> : null}
    {status === 'pending_review' && canReview && canPublish ? <Button type="primary" disabled={acting} onClick={() => action(() => api.approveConfigurationItem(namespaceId, itemId, revisionId), '已审核通过并发布')}>通过并发布</Button> : null}
    {data.current_published_revision_id && canUnpublish ? <Button danger disabled={acting} onClick={() => action(() => api.unpublishConfigurationItem(namespaceId, itemId, data.current_published_revision_id!), '配置已下线')}>下线</Button> : null}
  </Space> : null

  return <>
    <PageHeader eyebrow="配置值工作流" title={data?.item.display_name ?? '配置项'} description={data ? `${data.item.item_key} · ${data.item.description || '无说明'}` : '加载配置值。'} extra={<Space wrap><Button onClick={() => navigate(`/configurations/${namespaceId}`)}>返回配置项</Button>{actions}<Button type="primary" loading={saving} disabled={!canEdit || saving || acting || (data?.item.constraints.required && (value === null || value === ''))} onClick={save}>保存草稿</Button></Space>} />
    {status ? <div className="workflow-state"><Tag>{statusLabels[status]}</Tag><Typography.Text type="secondary">Revision {revision?.revision_number} · <code>{revisionId}</code></Typography.Text></div> : <Alert className="editor-notice" type="info" showIcon title="尚无 Revision" description="保存后创建第一个配置草稿。" />}
    {dirty ? <Alert className="editor-notice" type="warning" showIcon title="有未保存的修改" description="先保存新 Revision，再提交审核。" /> : null}
    {status === 'pending_review' ? <Alert className="editor-notice" type="warning" showIcon title="待审核 Revision 不可编辑" /> : null}
    {data?.item.status === 'archived' ? <Alert className="editor-notice" type="warning" showIcon title="归档配置不可编辑" /> : null}
    <DataState loading={itemValue.loading} error={itemValue.error} retry={itemValue.reload}>
      {data ? <section className="configuration-value-editor"><Typography.Text className="eyebrow">{data.item.value_type}</Typography.Text><ValueEditor type={data.item.value_type} value={value} onChange={setDraft} jsonText={jsonText} setJSONText={(text) => { setDraft(text); setJSONError('') }} jsonError={jsonError} disabled={!canEdit || saving || acting} principal={principal} targetModelId={data.item.constraints.target_model_id} /></section> : null}
    </DataState>
    <div className="configuration-history-grid">
      <section className="configuration-history" aria-label="Revision 历史">
        <Typography.Title level={3}>Revision 历史</Typography.Title>
        <DataState loading={revisions.loading} error={revisions.error} empty={!revisions.data?.items.length} retry={revisions.reload}>
          <div className="configuration-revision-list">{revisions.data?.items.map((item) => <article key={item.id} className="configuration-revision-item"><Space wrap><Tag>{statusLabels[item.workflow_status]}</Tag><strong>Revision {item.revision_number}</strong><Typography.Text type="secondary">{new Date(item.created_at).toLocaleString('zh-CN')}</Typography.Text></Space><Descriptions size="small" column={1} items={[{ key: 'id', label: 'ID', children: <code>{item.id}</code> }, { key: 'creator', label: '创建者', children: item.created_by }]} /><pre>{formatConfigurationValue(item.value)}</pre></article>)}</div>
        </DataState>
        <Space className="pagination-actions"><Button disabled={revisionCursors.length === 1} onClick={() => setRevisionCursors((values) => values.slice(0, -1))}>上一页 Revision</Button><Button disabled={!revisions.data?.next_cursor} onClick={() => revisions.data?.next_cursor && setRevisionCursors((values) => [...values, revisions.data!.next_cursor!])}>下一页 Revision</Button></Space>
      </section>
      <section className="configuration-history" aria-label="工作流事件">
        <Typography.Title level={3}>工作流事件</Typography.Title>
        <DataState loading={events.loading} error={events.error} empty={!events.data?.items.length} retry={events.reload}><Timeline items={events.data?.items.map((event) => ({ content: <div><strong>{eventLabels[event.type]}</strong><Descriptions size="small" column={1} items={[{ key: 'revision', label: 'Revision', children: <code>{event.revision_id}</code> }, { key: 'status', label: '状态', children: `${statusLabels[event.from_status]} → ${statusLabels[event.to_status]}` }, { key: 'actor', label: '操作者', children: event.actor_id }, { key: 'time', label: '时间', children: new Date(event.occurred_at).toLocaleString('zh-CN') }]} />{event.reason ? <Typography.Paragraph className="reject-reason">驳回原因：{event.reason}</Typography.Paragraph> : null}</div> }))} /></DataState>
        <Space className="pagination-actions"><Button disabled={eventCursors.length === 1} onClick={() => setEventCursors((values) => values.slice(0, -1))}>上一页事件</Button><Button disabled={!events.data?.next_cursor} onClick={() => events.data?.next_cursor && setEventCursors((values) => [...values, events.data!.next_cursor!])}>下一页事件</Button></Space>
      </section>
    </div>
    <Modal title="驳回配置 Revision" open={reasonOpen} onCancel={() => setReasonOpen(false)} okText="确认驳回" okButtonProps={{ danger: true, disabled: !reason.trim(), loading: acting }} onOk={async () => { if (await action(() => api.rejectConfigurationItem(namespaceId, itemId, revisionId, reason.trim()), '已驳回')) { setReasonOpen(false); setReason('') } }}><Input.TextArea aria-label="驳回理由" value={reason} maxLength={1000} rows={4} onChange={(event) => setReason(event.target.value)} /></Modal>
  </>
}

function formatConfigurationValue(value: unknown) {
  if (typeof value === 'bigint') return value.toString()
  if (typeof value === 'string') return value
  return JSON.stringify(value, null, 2)
}

function ValueEditor({ type, value, onChange, jsonText, setJSONText, jsonError, disabled, principal, targetModelId }: { type: ConfigurationValueType; value: unknown; onChange: (value: unknown) => void; jsonText: string; setJSONText: (value: string) => void; jsonError: string; disabled: boolean; principal: Principal; targetModelId?: string }) {
  if (type === 'string') return <Input.TextArea aria-label="配置值" rows={6} value={typeof value === 'string' ? value : ''} disabled={disabled} onChange={(event) => onChange(event.target.value)} />
  if (type === 'integer') return <Input aria-label="配置值" inputMode="numeric" value={typeof value === 'bigint' || typeof value === 'number' ? String(value) : ''} disabled={disabled} onChange={(event) => { const next = event.target.value.trim(); if (/^-?\d+$/.test(next)) onChange(BigInt(next)); else if (!next) onChange(null) }} />
  if (type === 'decimal') return <Input aria-label="配置值" value={typeof value === 'string' ? value : ''} disabled={disabled} placeholder="例如 12.50" onChange={(event) => onChange(event.target.value)} />
  if (type === 'boolean') return <Switch aria-label="配置值" checked={value === true} disabled={disabled} checkedChildren="是" unCheckedChildren="否" onChange={onChange} />
  if (type === 'json') return <><Input.TextArea className="configuration-json-input" aria-label="配置值 JSON" rows={14} value={jsonText} disabled={disabled} status={jsonError ? 'error' : undefined} onChange={(event) => setJSONText(event.target.value)} />{jsonError ? <Typography.Text type="danger">{jsonError}</Typography.Text> : null}</>
  if (type === 'single_asset' || type === 'multi_asset') return <AssetPicker multiple={type === 'multi_asset'} value={(type === 'multi_asset' ? Array.isArray(value) ? value : [] : typeof value === 'string' ? value : null) as string[] | string | null} onChange={onChange} disabled={disabled || !hasSystemPermission(principal, 'assets.view')} canUpload={hasSystemPermission(principal, 'assets.upload')} />
  const multiple = type === 'multi_relation'
  return <RelationPicker multiple={multiple} value={(multiple ? Array.isArray(value) ? value : [] : typeof value === 'string' ? value : null) as string[] | string | null} onChange={onChange} disabled={disabled} targetModelId={targetModelId} canViewTarget={Boolean(targetModelId && hasModelPermission(principal, targetModelId, 'content.view'))} />
}
