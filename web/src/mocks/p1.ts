const now = '2026-07-18T08:00:00Z'
const model = { id: 'mdl_articles', key: 'articles', display_name: '内部文章', description: '内部知识与公告', status: 'active', created_at: now, updated_at: now }
const fields = [
  { id: 'fld_title', key: 'title', display_name: '标题', description: '', type: 'single_line_text', required: true, default_value: null, constraints: { max_length: 120 }, children: [], status: 'active', created_at: now, updated_at: now },
  { id: 'fld_body', key: 'body', display_name: '正文', description: 'HTML 富文本', type: 'rich_text', required: false, default_value: null, constraints: {}, children: [], status: 'active', created_at: now, updated_at: now },
  { id: 'fld_cover', key: 'cover', display_name: '封面', description: '', type: 'single_media', required: false, default_value: null, constraints: {}, children: [], status: 'active', created_at: now, updated_at: now },
]
let entries = [{ id: 'ent_welcome', model_id: model.id, status: 'draft', current_draft_revision_id: 'rev_1', current_draft_content: { title: '欢迎使用', body: '', cover: null } as Record<string, unknown>, workflow_status: 'draft', current_published_revision_id: null, referenced_assets: {}, created_by: 'usr_dev_preview', created_at: now, updated_at: now }]
const roles = [{ id: 'rol_editor', key: 'editor', kind: 'custom', display_name: '内容编辑', description: '维护模型与草稿', system_permissions: ['models.view', 'models.update'], model_permissions: [{ model_id: model.id, permissions: ['content.view', 'content.create', 'content.update'] }], config_namespace_permissions: [], created_at: now, updated_at: now }]
const mockUser = { id: 'usr_dev_preview', display_name: '开发预览用户', email: null, phone_masked: '138****8000', auth_methods: ['sms'], is_emergency_admin: false, has_high_risk_role: false, status: 'enabled', created_at: now, updated_at: now }

export function enableP1Mock() {
  const nativeFetch = window.fetch.bind(window)
  window.fetch = async (input, init) => {
    const raw = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
    const url = new URL(raw, window.location.origin)
    if (!url.pathname.startsWith('/api/admin/v1/')) return nativeFetch(input, init)
    const path = url.pathname.slice('/api/admin/v1'.length)
    const method = (init?.method ?? 'GET').toUpperCase()
    if (path === '/users' && method === 'GET') return Response.json({ items: [mockUser], next_cursor: null })
    if (path === '/users' && method === 'POST') return Response.json({ ...mockUser, ...(JSON.parse(String(init?.body)) as { display_name: string; phone: string; role_ids: string[] }) }, { status: 201 })
    if (path === '/roles' && method === 'GET') return Response.json({ items: roles })
    if (path === '/users/usr_dev_preview' && method === 'GET') return Response.json({ ...mockUser, phone: '13800138000', role_ids: ['rol_editor'] })
    if (path === '/users/usr_dev_preview/phone' && method === 'PATCH') return Response.json(mockUser)
    if (path === '/users/usr_dev_preview/roles' && method === 'PUT') return Response.json({ ...mockUser, role_ids: (JSON.parse(String(init?.body)) as { role_ids: string[] }).role_ids })
    if (path === '/models' && method === 'GET') return Response.json({ items: [model] })
    if (path === `/models/${model.id}` && method === 'GET') return Response.json({ ...model, fields })
    if (path === `/models/${model.id}/entries` && method === 'GET') return Response.json({ items: entries, fields: fields.map(({ key, display_name, type, constraints, children }) => ({ key, display_name, type, constraints: { enum_options: 'enum_options' in constraints ? constraints.enum_options : undefined, filterable: false, sortable: false }, children })), next_cursor: null })
    if (path === `/models/${model.id}/entries/ent_welcome` && method === 'GET') return Response.json({ ...entries[0], current_draft_revision: { id: 'rev_1', entry_id: 'ent_welcome', model_id: model.id, number: 1, content: { title: '欢迎使用', body: '', cover: null }, created_by: 'usr_dev_preview', created_at: now } })
    if (path === '/audit/events' && method === 'GET') return Response.json({ items: [{ id: 'aud_1', occurred_at: now, request_id: 'req_dev_1', actor_type: 'user', actor_id: 'usr_dev_preview', actor_display_name: '开发预览用户', action: 'content_entry_created', resource_type: 'content_entry', resource_id: 'ent_welcome', result: 'success', ip: '127.0.0.1', user_agent: 'development mock', changes: { model_id: model.id }, failure_code: null }], next_cursor: null })
    if (path === `/models/${model.id}/entries` && method === 'POST') {
      const body = JSON.parse(String(init?.body)) as { content: Record<string, unknown> }
      const entry = { id: `ent_${entries.length + 1}`, model_id: model.id, status: 'draft' as const, current_draft_revision_id: `rev_${entries.length + 1}`, current_draft_content: body.content, workflow_status: 'draft', current_published_revision_id: null, referenced_assets: {}, created_by: 'usr_dev_preview', created_at: now, updated_at: now }
      entries = [entry, ...entries]
      return Response.json({ ...entry, current_draft_revision: { id: entry.current_draft_revision_id, entry_id: entry.id, model_id: model.id, number: 1, content: body.content, created_by: entry.created_by, created_at: now } }, { status: 201 })
    }
    return nativeFetch(input, init)
  }
}
