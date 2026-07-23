import { Alert, Button, Descriptions, Input, Modal, Space, Tag, Timeline, Tooltip, Typography, message } from 'antd'
import { useRef, useState } from 'react'
import { useBeforeUnload, useBlocker, useNavigate, useParams } from 'react-router-dom'

import { ApiError, api, apiErrorMessage } from '../api/client'
import type { ContentEntry, Principal, WorkflowEvent, WorkflowStatus } from '../api/types'
import { hasModelPermission, hasSystemPermission } from '../auth/permissions'
import { DynamicContentForm } from '../components/DynamicContentForm'
import { DataState, PageHeader, PendingApiNotice, useApiData } from '../components/Page'

const labels: Record<WorkflowStatus, string> = { draft: '草稿', pending_review: '待审核', rejected: '已驳回', published: '已发布', unpublished: '已下线' }
const eventLabels: Record<WorkflowEvent['type'], string> = { submitted: '提交审核', approved: '审核通过并发布', rejected: '驳回', unpublished: '下线' }

export default function EntryEditorPage({ principal }: { principal: Principal }) {
  const { modelId = '', entryId } = useParams()
  return <EntryEditor key={`${modelId}:${entryId ?? 'new'}`} principal={principal} modelId={modelId} entryId={entryId} />
}

function EntryEditor({ principal, modelId, entryId }: { principal: Principal; modelId: string; entryId?: string }) {
  const navigate = useNavigate()
  const canViewModel = hasSystemPermission(principal, 'models.view')
  const model = useApiData(() => canViewModel ? api.getModel(modelId) : Promise.resolve(undefined), [modelId, canViewModel])
  const entry = useApiData(() => entryId ? api.getEntry(modelId, entryId) : Promise.resolve(undefined), [modelId, entryId])
  const [eventCursors, setEventCursors] = useState<Array<string | undefined>>([undefined])
  const eventCursor = eventCursors.at(-1)
  const events = useApiData(() => entryId ? api.listWorkflowEvents(modelId, entryId, eventCursor) : Promise.resolve({ items: [], next_cursor: null }), [modelId, entryId, eventCursor])
  const [draft, setDraft] = useState<Record<string, unknown>>()
  const [reasonOpen, setReasonOpen] = useState(false)
  const [reason, setReason] = useState('')
  const [acting, setActing] = useState(false)
  const [saving, setSaving] = useState(false)
  const [saveFailed, setSaveFailed] = useState(false)
  const [fieldValidity, setFieldValidity] = useState<Record<string, boolean>>({})
  const writeLock = useRef(false)
  const allowNavigation = useRef(false)
  const content = draft ?? entry.data?.current_draft_revision.content ?? {}
  const workflowStatus = entry.data?.workflow_status
  const canWrite = hasModelPermission(principal, modelId, entryId ? 'content.update' : 'content.create')
  const canSubmit = hasModelPermission(principal, modelId, 'content.submit')
  const canReview = hasModelPermission(principal, modelId, 'content.review')
  const canPublish = hasModelPermission(principal, modelId, 'content.publish')
  const canUnpublish = hasModelPermission(principal, modelId, 'content.unpublish')
  const editable = !entryId || !workflowStatus || ['draft', 'rejected', 'published', 'unpublished'].includes(workflowStatus)
  const hasFields = Boolean(model.data?.fields.length)
  const canEditStructure = canViewModel && hasFields
  const hasUnsavedChanges = draft !== undefined
  const writing = saving || acting
  const formValid = Object.values(fieldValidity).every(Boolean)
  const blocker = useBlocker(({ currentLocation, nextLocation }) => hasUnsavedChanges && !allowNavigation.current && `${currentLocation.pathname}${currentLocation.search}${currentLocation.hash}` !== `${nextLocation.pathname}${nextLocation.search}${nextLocation.hash}`)
  useBeforeUnload((event) => {
    if (!hasUnsavedChanges) return
    event.preventDefault()
    event.returnValue = ''
  })

  function reloadEvents() {
    setEventCursors([undefined])
    events.reload()
  }

  function applyResult(result: ContentEntry, success: string) {
    entry.setData(result)
    setDraft(undefined)
    reloadEvents()
    message.success(success)
  }

  async function action(run: () => Promise<ContentEntry>, success: string) {
    if (writeLock.current) return false
    writeLock.current = true
    setActing(true)
    try {
      applyResult(await run(), success)
      return true
    } catch (error) {
      if (error instanceof ApiError && ['workflow_revision_conflict', 'invalid_workflow_transition'].includes(error.code)) message.error('版本状态已变化，请重新载入')
      else message.error(apiErrorMessage(error, '工作流操作失败'))
      return false
    } finally {
      setActing(false)
      writeLock.current = false
    }
  }

  async function save() {
    if (writeLock.current) return
    writeLock.current = true
    setSaving(true)
    try {
      const result = entryId && entry.data ? await api.updateEntry(modelId, entryId, entry.data.current_draft_revision_id, content) : await api.createEntry(modelId, content)
      message.success('草稿已保存为新 Revision')
      setSaveFailed(false)
      setDraft(undefined)
      if (!entryId) {
        allowNavigation.current = true
        navigate(`/content/${modelId}/${result.id}`, { replace: true })
      }
      else {
        entry.setData(result)
        reloadEvents()
      }
    } catch (error) {
      setSaveFailed(true)
      if (error instanceof ApiError && error.code === 'draft_revision_conflict') message.error('草稿已被其他人更新，请重新载入后再编辑')
      else message.error(apiErrorMessage(error, '保存草稿失败'))
    } finally {
      setSaving(false)
      writeLock.current = false
    }
  }

  const revisionId = entry.data?.current_draft_revision_id ?? ''
  const actionButtons = entryId && entry.data ? <Space wrap className="workflow-actions">
    {workflowStatus === 'draft' && canSubmit ? <Tooltip title={hasUnsavedChanges ? '请先保存当前修改，生成新 Revision 后再提交审核' : undefined}><span><Button type="primary" loading={acting} disabled={hasUnsavedChanges || writing} onClick={() => action(() => api.submitEntry(modelId, entryId, revisionId), '已提交审核')}>提交审核</Button></span></Tooltip> : null}
    {workflowStatus === 'pending_review' && canReview ? <Button danger loading={acting} disabled={writing} onClick={() => setReasonOpen(true)}>驳回</Button> : null}
    {workflowStatus === 'pending_review' && canReview && canPublish ? <Button type="primary" loading={acting} disabled={writing} onClick={() => action(() => api.approveEntry(modelId, entryId, revisionId), '已审核通过并发布')}>通过并发布</Button> : null}
    {workflowStatus === 'published' && canUnpublish && entry.data.current_published_revision_id ? <Button danger loading={acting} disabled={writing} onClick={() => action(() => api.unpublishEntry(modelId, entryId, entry.data!.current_published_revision_id!), '内容已下线')}>下线</Button> : null}
  </Space> : null

  return <>
    <PageHeader eyebrow="版本工作流" title={entryId ? '内容与审核' : '新建草稿'} description="每个动作锁定明确 Revision，工作流事件不可变并保留驳回理由。" extra={<Space wrap><Typography.Text className="save-state" type={saveFailed ? 'danger' : hasUnsavedChanges ? 'warning' : 'secondary'}>{saving ? '正在保存' : saveFailed ? '保存失败，本地修改仍保留' : hasUnsavedChanges ? '有未保存修改' : entryId ? '已保存' : '尚未保存'}</Typography.Text><Button disabled={writing} onClick={() => navigate(`/content/${modelId}`)}>返回列表</Button>{actionButtons}<Button type="primary" loading={saving} disabled={writing || !canWrite || !canEditStructure || !editable || !formValid || entry.data?.status === 'archived'} onClick={save}>保存草稿</Button></Space>} />
    <PendingApiNotice />
    {workflowStatus ? <div className="workflow-state"><Tag>{labels[workflowStatus]}</Tag><Typography.Text type="secondary">工作头 <code>{revisionId}</code></Typography.Text></div> : null}
    {hasUnsavedChanges ? <Alert className="editor-notice" type="warning" showIcon title="有未保存的修改" description="请先保存为新 Revision，才能提交审核；当前已保存的旧 Revision 不会被送审。" /> : null}
    {!canViewModel ? <Alert className="editor-notice" type="info" showIcon title="无内容模型结构权限" description="当前内容以只读原始数据展示。仍可查看审核详情并执行已授权的工作流操作，但不能编辑内容。" /> : null}
    {canViewModel && !model.loading && !model.error && !hasFields ? <Alert className="editor-notice" type="warning" showIcon title="模型没有字段定义" description="当前 Revision 仅以只读原始数据展示，不能将空表单保存为内容。" /> : null}
    {workflowStatus === 'published' ? <Alert className="editor-notice" type="info" showIcon title="正在编辑已发布内容" description="保存只会创建新草稿，线上版本保持不变，直到新版本通过审核。" /> : null}
    {workflowStatus === 'pending_review' ? <Alert className="editor-notice" type="warning" showIcon title="待审核版本不可编辑" description="请通过或驳回当前版本。" /> : null}
    {entry.data?.status === 'archived' ? <Alert type="warning" showIcon title="归档内容不可编辑" /> : null}
    <div className="entry-workspace">
      <DataState loading={entry.loading || (canViewModel && model.loading)} error={entry.error ?? (canViewModel ? model.error : undefined)} retry={() => { model.reload(); entry.reload() }}>
        {canEditStructure ? <DynamicContentForm fields={model.data!.fields} content={content} onChange={setDraft} disabled={writing || !canWrite || !editable || entry.data?.status === 'archived'} canSelectAssets={hasSystemPermission(principal, 'assets.view')} canUploadAssets={hasSystemPermission(principal, 'assets.view') && hasSystemPermission(principal, 'assets.upload')} referencedAssets={entry.data?.referenced_assets} canViewModel={(targetModelId) => hasModelPermission(principal, targetModelId, 'content.view')} onFieldValidityChange={(path, valid) => setFieldValidity((current) => current[path] === valid ? current : { ...current, [path]: valid })} modelId={modelId} entryId={entryId} /> : entry.data ? <pre aria-label="只读内容数据">{JSON.stringify(entry.data.current_draft_revision.content, null, 2)}</pre> : null}
      </DataState>
      {entryId ? <aside className="workflow-history" aria-label="版本工作流事件">
        <Typography.Title level={3}>版本事件</Typography.Title>
        <DataState loading={events.loading} error={events.error} empty={!events.data?.items.length} retry={events.reload}><Timeline items={events.data?.items.map((event) => ({ content: <div><strong>{eventLabels[event.type]}</strong><Descriptions size="small" column={1} items={[{ key: 'revision', label: 'Revision', children: <code>{event.revision_id}</code> }, { key: 'actor', label: '操作者', children: event.actor_id }, { key: 'time', label: '时间', children: new Date(event.occurred_at).toLocaleString('zh-CN') }]} />{event.reason ? <Typography.Paragraph>理由：{event.reason}</Typography.Paragraph> : null}</div> }))} /></DataState>
        <Space className="pagination-actions"><Button disabled={eventCursors.length === 1} onClick={() => setEventCursors((values) => values.slice(0, -1))}>上一页事件</Button><Button disabled={!events.data?.next_cursor} onClick={() => events.data?.next_cursor && setEventCursors((values) => [...values, events.data!.next_cursor!])}>下一页事件</Button></Space>
      </aside> : null}
    </div>
    <Modal title="驳回版本" open={reasonOpen} onCancel={() => setReasonOpen(false)} okText="确认驳回" okButtonProps={{ danger: true, disabled: !reason.trim() || writing, loading: acting }} onOk={async () => { if (await action(() => api.rejectEntry(modelId, entryId!, revisionId, reason.trim()), '已驳回')) { setReasonOpen(false); setReason('') } }}><Input.TextArea aria-label="驳回理由" value={reason} maxLength={1000} rows={4} onChange={(event) => setReason(event.target.value)} placeholder="必填，说明需要修改的内容" /></Modal>
    <Modal title="放弃未保存的修改？" open={blocker.state === 'blocked'} okText="放弃并离开" cancelText="继续编辑" okButtonProps={{ danger: true }} onOk={() => blocker.proceed?.()} onCancel={() => blocker.reset?.()}>当前修改尚未保存，离开后无法恢复。</Modal>
  </>
}
