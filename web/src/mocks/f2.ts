const now = '2026-07-19T08:00:00Z'
const model = { id: 'mdl_articles', key: 'articles', display_name: '内部文章', description: '内部知识与公告', status: 'active', created_at: now, updated_at: now }
const fields = [
  { id: 'fld_title', key: 'title', display_name: '标题', description: '', type: 'single_line_text', required: true, default_value: null, constraints: { max_length: 120, filterable: true, sortable: true }, children: [], status: 'active', created_at: now, updated_at: now },
  { id: 'fld_related', key: 'related', display_name: '相关文章', description: '关联内容条目', type: 'multi_relation', required: false, default_value: null, constraints: { target_model_id: model.id }, children: [], status: 'active', created_at: now, updated_at: now },
  { id: 'fld_cover', key: 'cover', display_name: '封面', description: '', type: 'single_media', required: false, default_value: null, constraints: {}, children: [], status: 'active', created_at: now, updated_at: now },
]
let entry: {
  id: string; model_id: string; status: 'draft'; workflow_status: WorkflowStatus
  current_draft_revision_id: string; current_published_revision_id: string | null
  current_draft_content: Record<string, unknown>; referenced_assets: Record<string, ReferencedAsset>
  created_by: string; created_at: string; updated_at: string
} = {
  id: 'ent_review', model_id: model.id, status: 'draft', workflow_status: 'pending_review', current_draft_revision_id: 'rev_2', current_published_revision_id: null,
  current_draft_content: { title: '阶段二审核说明', related: ['ent_welcome'], cover: null }, referenced_assets: {},
  created_by: 'usr_editor', created_at: now, updated_at: now,
}
const revision: WorkflowRevision = { id: 'rev_2', entry_id: entry.id, model_id: model.id, number: 2, content: { title: '阶段二审核说明', related: ['ent_welcome'], cover: null }, created_by: 'usr_editor', created_at: now, workflow_status: 'pending_review', submitted_by: 'usr_editor', submitted_at: now }
let events: WorkflowEvent[] = [{ id: 'evt_1', entry_id: entry.id, revision_id: revision.id, type: 'submitted', from_status: 'draft', to_status: 'pending_review', actor_id: 'usr_editor', reason: null, occurred_at: now }]
let keys: APIKey[] = [{ id: 'key_1', name: '官网读取', prefix: 'abcd2345wxyz', model_ids: [model.id], status: 'active', expires_at: null, revoked_at: null, last_used_at: null, rotated_from_id: null, replaced_by_id: null, created_by: 'usr_dev_preview', created_at: now }]

function requestBody(init?: RequestInit) { return JSON.parse(String(init?.body ?? '{}')) as Record<string, unknown> }
function secret(id: string, name: string, modelIds: string[], rotatedFromId: string | null = null): APIKeySecret {
  return { id, name, prefix: 'newkey234567', model_ids: modelIds, status: 'active', expires_at: null, revoked_at: null, last_used_at: null, rotated_from_id: rotatedFromId, replaced_by_id: null, created_by: 'usr_dev_preview', created_at: now, key: 'cmsk_newkey234567_abcdefghijklmnopqrstuvwxyzABCDEFGH123456789' }
}
function metadata(value: APIKeySecret): APIKey {
  const { key, ...result } = value
  void key
  return result
}

export function enableF2Mock() {
  const nativeFetch = window.fetch.bind(window)
  window.fetch = async (input, init) => {
    const raw = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
    const url = new URL(raw, window.location.origin)
    if (!url.pathname.startsWith('/api/admin/v1/')) return nativeFetch(input, init)
    const path = url.pathname.slice('/api/admin/v1'.length)
    const method = (init?.method ?? 'GET').toUpperCase()
    if (path === '/models' && method === 'GET') return Response.json({ items: [model] })
    if (path === `/models/${model.id}` && method === 'GET') return Response.json({ ...model, fields })
    if (path === `/models/${model.id}/entries` && method === 'GET') {
      const status = url.searchParams.get('workflow_status')
      const items = status && status !== entry.workflow_status ? [] : [entry]
      return Response.json({ items, fields: fields.map(({ key, display_name, type, constraints, children }) => ({ key, display_name, type, constraints: { filterable: 'filterable' in constraints && constraints.filterable === true, sortable: 'sortable' in constraints && constraints.sortable === true }, children })), next_cursor: null, total: items.length, total_is_estimate: false })
    }
    if (path === `/models/${model.id}/entries/${entry.id}` && method === 'GET') return Response.json({ ...entry, current_draft_revision: { ...revision, workflow_status: entry.workflow_status }, current_published_revision: null })
    if (path === `/models/${model.id}/entries/${entry.id}/workflow-events` && method === 'GET') return Response.json({ items: events, next_cursor: null })
    const action = path.match(new RegExp(`^/models/${model.id}/entries/${entry.id}/(submit|approve|reject|unpublish)$`))?.[1]
    if (action && method === 'POST') {
      const body = requestBody(init)
      const transitions = { submit: ['submitted', 'pending_review'], approve: ['approved', 'published'], reject: ['rejected', 'rejected'], unpublish: ['unpublished', 'unpublished'] } as const
      const from = entry.workflow_status
      const [type, to] = transitions[action as keyof typeof transitions]
      entry = { ...entry, workflow_status: to, current_published_revision_id: to === 'published' ? revision.id : null, updated_at: now }
      events = [{ id: `evt_${events.length + 1}`, entry_id: entry.id, revision_id: String(body.revision_id), type, from_status: from, to_status: to, actor_id: 'usr_dev_preview', reason: typeof body.reason === 'string' ? body.reason : null, occurred_at: now }, ...events]
      return Response.json({ ...entry, current_draft_revision: { ...revision, workflow_status: to }, current_published_revision: to === 'published' ? { ...revision, workflow_status: to } : null })
    }
    if (path === '/api-keys' && method === 'GET') {
      const status = url.searchParams.get('status')
      return Response.json({ items: status ? keys.filter((key) => key.status === status) : keys, next_cursor: null })
    }
    if (path === '/api-keys' && method === 'POST') {
      const body = requestBody(init); const created = secret(`key_${keys.length + 1}`, String(body.name), body.model_ids as string[])
      keys = [metadata(created), ...keys]
      return Response.json(created, { status: 201, headers: { 'Cache-Control': 'no-store' } })
    }
    const keyAction = path.match(/^\/api-keys\/([^/]+)(\/rotate)?$/)
    if (keyAction && method === 'DELETE') {
      keys = keys.map((key) => key.id === keyAction[1] ? { ...key, status: 'revoked', revoked_at: now } : key)
      return new Response(null, { status: 204 })
    }
    if (keyAction?.[2] && method === 'POST') {
      const old = keys.find((key) => key.id === keyAction[1])!
      const created = secret(`key_${keys.length + 1}`, old.name, old.model_ids, old.id)
      keys = [metadata(created), ...keys.map((key) => key.id === old.id ? { ...key, status: 'revoked' as const, revoked_at: now, replaced_by_id: created.id } : key)]
      return Response.json(created, { status: 201, headers: { 'Cache-Control': 'no-store' } })
    }
    return nativeFetch(input, init)
  }
}
import type { APIKey, APIKeySecret, ReferencedAsset, WorkflowEvent, WorkflowRevision, WorkflowStatus } from '../api/types'
