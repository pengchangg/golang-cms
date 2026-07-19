import { Alert, Button, Space, message } from 'antd'
import { useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'

import { ApiError, api } from '../api/client'
import type { Principal } from '../api/types'
import { hasModelPermission } from '../auth/permissions'
import { DynamicContentForm } from '../components/DynamicContentForm'
import { DataState, PageHeader, PendingApiNotice, useApiData } from '../components/Page'

export default function EntryEditorPage({ principal }: { principal: Principal }) {
  const { modelId = '', entryId } = useParams(); const navigate = useNavigate()
  const model = useApiData(() => api.getModel(modelId), [modelId]); const entry = useApiData(() => entryId ? api.getEntry(modelId, entryId) : Promise.resolve(undefined), [modelId, entryId])
  const [draft, setDraft] = useState<Record<string, unknown>>()
  const content = draft ?? entry.data?.current_draft_revision.content ?? {}
  const canWrite = hasModelPermission(principal, modelId, entryId ? 'content.update' : 'content.create')
  async function save() {
    try {
      const result = entryId && entry.data ? await api.updateEntry(modelId, entryId, entry.data.current_draft_revision_id, content) : await api.createEntry(modelId, content)
      message.success('草稿已保存为新 Revision'); setDraft(undefined)
      if (!entryId) navigate(`/content/${modelId}/${result.id}`, { replace: true }); else entry.setData(result)
    } catch (error) {
      if (error instanceof ApiError && error.code === 'draft_revision_conflict') message.error('草稿已被其他人更新，请重新载入后再编辑')
      else throw error
    }
  }
  return <><PageHeader eyebrow="动态内容编辑" title={entryId ? '编辑草稿' : '新建草稿'} description="字段由当前模型动态渲染，保存提交完整 content 和当前基线 Revision。" extra={<Space><Button onClick={() => navigate(`/content/${modelId}`)}>返回列表</Button><Button type="primary" disabled={!canWrite} onClick={save}>保存草稿</Button></Space>} /><PendingApiNotice />{entry.data?.status === 'archived' ? <Alert type="warning" showIcon title="归档内容不可编辑" /> : null}<DataState loading={model.loading || entry.loading} error={model.error ?? entry.error} empty={!model.data?.fields.length} retry={() => { model.reload(); entry.reload() }}><DynamicContentForm fields={model.data?.fields ?? []} content={content} onChange={setDraft} disabled={!canWrite || entry.data?.status === 'archived'} /></DataState></>
}
