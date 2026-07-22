import { afterEach, describe, expect, it, vi } from 'vitest'

import { authStore } from '../auth/store'
import { ApiError, api, safeReturnTo, uploadAssetFile } from './client'
import type { SessionResponse } from './types'

const session: SessionResponse = {
  principal: { user_id: 'usr_test', display_name: '测试用户', email: null, auth_method: 'local', is_emergency_admin: false, has_high_risk_role: false, system_permissions: [], model_permissions: [] },
  content_models: [],
  csrf_token: 'csrf-token-with-at-least-thirty-two-characters',
  idle_expires_at: '2026-07-18T10:00:00Z',
  expires_at: '2026-07-18T20:00:00Z',
}

afterEach(() => {
  vi.unstubAllGlobals()
  authStore.reset()
})

describe('API Client', () => {
  it('固定同源凭据并为写请求附加内存中的 CSRF Token', async () => {
    authStore.setSession(session)
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)

    await api.logout()

    expect(fetchMock).toHaveBeenCalledWith('/api/admin/v1/auth/logout', expect.objectContaining({ credentials: 'same-origin', method: 'POST' }))
    const headers = fetchMock.mock.calls[0][1].headers as Headers
    expect(headers.get('X-CSRF-Token')).toBe(session.csrf_token)
  })

  it('CSV 导入使用 FormData boundary 并沿用 CSRF 与错误解析', async () => {
    authStore.setSession(session)
    const fetchMock = vi.fn().mockResolvedValueOnce(Response.json({ imported: 2 }))
      .mockResolvedValueOnce(Response.json({ error: { code: 'validation_failed', message: 'CSV 校验失败', request_id: 'req_csv', details: [] } }, { status: 400 }))
    vi.stubGlobal('fetch', fetchMock)
    const file = new File(['title\n示例'], 'entries.csv', { type: 'text/csv' })

    await expect(api.importCSV('mdl/a', file)).resolves.toEqual({ imported: 2 })
    const init = fetchMock.mock.calls[0][1] as RequestInit
    const headers = init.headers as Headers
    expect(fetchMock.mock.calls[0][0]).toBe('/api/admin/v1/models/mdl%2Fa/imports')
    expect(init.body).toBeInstanceOf(FormData)
    expect((init.body as FormData).get('file')).toBe(file)
    expect(headers.has('Content-Type')).toBe(false)
    expect(headers.get('X-CSRF-Token')).toBe(session.csrf_token)
    await expect(api.importCSV('mdl/a', file)).rejects.toMatchObject({ code: 'validation_failed', requestId: 'req_csv' })
  })

  it('CSV 导出编码查询并返回 Blob', async () => {
    const responseBlob = new Blob(['title\n示例'], { type: 'text/csv' })
    const fetchMock = vi.fn().mockResolvedValue(new Response(responseBlob, { headers: { 'Content-Type': 'text/csv' } }))
    vi.stubGlobal('fetch', fetchMock)

    const result = await api.exportCSV('mdl/a', { workflow_status: 'draft', filter: '{"title":{"eq":"示例"}}', sort: '-title' })

    expect(fetchMock.mock.calls[0][0]).toBe('/api/admin/v1/models/mdl%2Fa/exports.csv?workflow_status=draft&filter=%7B%22title%22%3A%7B%22eq%22%3A%22%E7%A4%BA%E4%BE%8B%22%7D%7D&sort=-title')
    expect(result).toBeInstanceOf(Blob)
    expect(result.type).toBe('text/csv')
    expect(result.size).toBeGreaterThan(0)
  })

  it('素材上传仅在对象存储 PUT 成功后确认并返回可用素材', async () => {
    const file = new File([new Uint8Array([1, 2, 3])], 'cover.png', { type: 'image/png' })
    Object.defineProperty(file, 'arrayBuffer', { value: async () => new Uint8Array([1, 2, 3]).buffer })
    const asset = { id: 'ast_1', filename: file.name, mime_type: file.type, preview_kind: 'image' as const, size: file.size, sha256: 'a'.repeat(64), etag: 'etag', status: 'available' as const, created_by: 'usr_1', created_at: '2026-07-20T08:00:00Z', confirmed_at: '2026-07-20T08:00:00Z', archived_at: null }
    const create = vi.spyOn(api, 'createAssetUpload').mockResolvedValue({ asset: { ...asset, status: 'quarantined', etag: null, confirmed_at: null }, upload: { method: 'PUT', url: 'https://bucket.example.com/upload', headers: { 'Content-Type': file.type }, expires_at: '2026-07-20T08:15:00Z' } })
    const confirm = vi.spyOn(api, 'confirmAssetUpload').mockResolvedValue(asset)
    const put = vi.fn().mockResolvedValue(new Response(null, { status: 200 }))
    vi.stubGlobal('fetch', put)

    await expect(uploadAssetFile(file)).resolves.toEqual(asset)
    expect(create).toHaveBeenCalledWith(expect.objectContaining({ filename: file.name, mime_type: file.type, size: file.size, sha256: expect.stringMatching(/^[a-f0-9]{64}$/) }))
    expect(put).toHaveBeenCalledWith('https://bucket.example.com/upload', expect.objectContaining({ method: 'PUT', body: file }))
    expect(confirm).toHaveBeenCalledWith('ast_1')
    expect(create.mock.invocationCallOrder[0]).toBeLessThan(put.mock.invocationCallOrder[0])
    expect(put.mock.invocationCallOrder[0]).toBeLessThan(confirm.mock.invocationCallOrder[0])
  })

  it('素材确认未进入可用状态时不返回可选择素材', async () => {
    const file = new File([new Uint8Array([1])], 'cover.png', { type: 'image/png' })
    Object.defineProperty(file, 'arrayBuffer', { value: async () => new Uint8Array([1]).buffer })
    const quarantined = { id: 'ast_1', filename: file.name, mime_type: file.type, preview_kind: 'image' as const, size: file.size, sha256: 'a'.repeat(64), etag: null, status: 'quarantined' as const, created_by: 'usr_1', created_at: '2026-07-20T08:00:00Z', confirmed_at: null, archived_at: null }
    vi.spyOn(api, 'createAssetUpload').mockResolvedValue({ asset: quarantined, upload: { method: 'PUT', url: 'https://bucket.example.com/upload', headers: {}, expires_at: '2026-07-20T08:15:00Z' } })
    vi.spyOn(api, 'confirmAssetUpload').mockResolvedValue(quarantined)
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 200 })))
    await expect(uploadAssetFile(file)).rejects.toThrow('确认后仍不可用')
  })

  it('会话失效时清空认证状态并保留 request_id', async () => {
    authStore.setSession(session)
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(Response.json({ error: { code: 'session_invalid', message: '会话失效', request_id: 'req_401', details: [] } }, { status: 401 })))

    await expect(api.getSession()).rejects.toMatchObject({ code: 'session_invalid', requestId: 'req_401' } satisfies Partial<ApiError>)
    expect(authStore.getSnapshot().status).toBe('anonymous')
  })

  it('旧请求的会话失效响应不能清空新会话', async () => {
    authStore.setSession(session)
    let resolve!: (response: Response) => void
    vi.stubGlobal('fetch', vi.fn().mockReturnValue(new Promise<Response>((done) => { resolve = done })))
    const request = api.getSession()
    const current = { ...session, csrf_token: 'new-session-csrf-token-with-thirty-two-chars' }
    authStore.setSession(current)
    resolve(Response.json({ error: { code: 'session_invalid', message: '旧会话失效', request_id: 'req_old', details: [] } }, { status: 401 }))

    await expect(request).rejects.toMatchObject({ code: 'session_invalid' })
    expect(authStore.getSnapshot()).toMatchObject({ status: 'authenticated', session: current })
  })

  it('后发认证操作会废弃先发操作的成功响应', () => {
    const first = authStore.beginTransition()
    const second = authStore.beginTransition()
    const newer = { ...session, csrf_token: 'newer-session-csrf-token-with-thirty-two-chars' }

    expect(authStore.setSession(session, first)).toBe(false)
    expect(authStore.setSession(newer, second)).toBe(true)
    expect(authStore.getSnapshot()).toMatchObject({ status: 'authenticated', session: newer })
  })

  it('403 只作为授权错误，不清空有效认证状态', async () => {
    authStore.setSession(session)
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(Response.json({ error: { code: 'permission_denied', message: '无权操作', request_id: 'req_403', details: [] } }, { status: 403 })))

    await expect(api.logout()).rejects.toMatchObject({ status: 403, code: 'permission_denied' })
    expect(authStore.getSnapshot().status).toBe('authenticated')
  })

  it('只接受同源绝对 returnTo 并保留查询与哈希', () => {
    expect(safeReturnTo('/articles?status=draft#editor')).toBe('/articles?status=draft#editor')
    expect(safeReturnTo('//outside.example')).toBeNull()
    expect(safeReturnTo('https://outside.example')).toBeNull()
    expect(safeReturnTo('/\\outside.example')).toBeNull()
  })

  it('用户筛选和草稿更新严格使用冻结路径与 DTO', async () => {
    authStore.setSession(session)
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(Response.json({ items: [], next_cursor: null }))
      .mockResolvedValueOnce(Response.json({ id: 'ent_1' }))
    vi.stubGlobal('fetch', fetchMock)

    await api.listUsers({ status: 'enabled', query: '林 岚' })
    await api.updateEntry('mdl/a', 'ent/b', 'rev_1', { title: '草稿' })

    expect(fetchMock.mock.calls[0][0]).toBe('/api/admin/v1/users?status=enabled&query=%E6%9E%97+%E5%B2%9A')
    expect(fetchMock.mock.calls[1][0]).toBe('/api/admin/v1/models/mdl%2Fa/entries/ent%2Fb')
    expect(fetchMock.mock.calls[1][1]).toEqual(expect.objectContaining({ method: 'PATCH', body: JSON.stringify({ base_revision_id: 'rev_1', content: { title: '草稿' } }) }))
  })

  it('短信登录和手机号账户管理使用批准的路径与 DTO', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(Response.json({ challenge_id: 'cap_1' }))
      .mockResolvedValueOnce(Response.json({ challenge_id: 'sms_1' }))
      .mockResolvedValueOnce(Response.json(session))
      .mockResolvedValueOnce(Response.json({ id: 'usr_1' }, { status: 201 }))
      .mockResolvedValueOnce(Response.json({ id: 'usr_1' }))
    vi.stubGlobal('fetch', fetchMock)

    await api.createCaptchaChallenge()
    await api.createSMSChallenge({ phone: '13800138000', captcha_challenge_id: 'cap_1', captcha_x: 80, captcha_y: 72 })
    await api.verifySMSChallenge('sms/a', '123456')
    await api.createUser({ display_name: '林岚', phone: '13800138000', role_ids: ['rol_1'] })
    await api.updateUserPhone('usr/a', '13900139000')

    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      '/api/admin/v1/auth/captcha/challenges',
      '/api/admin/v1/auth/sms/challenges',
      '/api/admin/v1/auth/sms/challenges/sms%2Fa/verify',
      '/api/admin/v1/users',
      '/api/admin/v1/users/usr%2Fa/phone',
    ])
    expect(fetchMock.mock.calls[1][1]).toEqual(expect.objectContaining({ method: 'POST', body: JSON.stringify({ phone: '13800138000', captcha_challenge_id: 'cap_1', captcha_x: 80, captcha_y: 72 }) }))
    expect(fetchMock.mock.calls[3][1]).toEqual(expect.objectContaining({ body: JSON.stringify({ display_name: '林岚', phone: '13800138000', role_ids: ['rol_1'] }) }))
    expect(fetchMock.mock.calls[4][1]).toEqual(expect.objectContaining({ method: 'PATCH', body: JSON.stringify({ phone: '13900139000' }) }))
  })

  it('工作流动作显式携带目标 Revision 和驳回理由', async () => {
    authStore.setSession(session)
    const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(Response.json({ id: 'ent_1' })))
    vi.stubGlobal('fetch', fetchMock)

    await api.submitEntry('mdl/a', 'ent/b', 'rev_2')
    await api.approveEntry('mdl/a', 'ent/b', 'rev_2')
    await api.rejectEntry('mdl/a', 'ent/b', 'rev_2', '信息来源不足')
    await api.unpublishEntry('mdl/a', 'ent/b', 'rev_2')

    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      '/api/admin/v1/models/mdl%2Fa/entries/ent%2Fb/submit',
      '/api/admin/v1/models/mdl%2Fa/entries/ent%2Fb/approve',
      '/api/admin/v1/models/mdl%2Fa/entries/ent%2Fb/reject',
      '/api/admin/v1/models/mdl%2Fa/entries/ent%2Fb/unpublish',
    ])
    expect(fetchMock.mock.calls[2][1]).toEqual(expect.objectContaining({ method: 'POST', body: JSON.stringify({ revision_id: 'rev_2', reason: '信息来源不足' }) }))
  })

  it('内容列表按 F2 编码过滤排序参数', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(Response.json({ items: [], next_cursor: null })))
    await api.listEntries('mdl_1', { workflow_status: 'pending_review', filter: '{"title":{"eq":"公告"}}', relation_filter: '{"author":{"contains":"ent_1"}}', sort: '-title', include_total: true })
    expect(fetch).toHaveBeenCalledWith('/api/admin/v1/models/mdl_1/entries?workflow_status=pending_review&filter=%7B%22title%22%3A%7B%22eq%22%3A%22%E5%85%AC%E5%91%8A%22%7D%7D&relation_filter=%7B%22author%22%3A%7B%22contains%22%3A%22ent_1%22%7D%7D&sort=-title&include_total=true', expect.any(Object))
  })

  it('字段查询、创建子字段、修改、归档和排序使用冻结路径与请求体', async () => {
    authStore.setSession(session)
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(Response.json({ items: [] }))
      .mockResolvedValueOnce(Response.json({ id: 'fld_1' }))
      .mockResolvedValueOnce(Response.json({ id: 'fld_1' }))
      .mockResolvedValueOnce(Response.json({ id: 'fld_child' }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)

    await api.listFields('mdl/a')
    await api.getField('mdl/a', 'fld/b')
    await api.updateField('mdl/a', 'fld/b', { display_name: '新标题' })
    await api.createChildField('mdl/a', 'fld/root', { key: 'child', display_name: '子字段', type: 'single_line_text' })
    await api.archiveField('mdl/a', 'fld/b')
    await api.reorderFields('mdl/a', { parent_id: 'fld/root', base_field_ids: ['fld/b', 'fld/c'], field_ids: ['fld/c', 'fld/b'] })

    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      '/api/admin/v1/models/mdl%2Fa/fields',
      '/api/admin/v1/models/mdl%2Fa/fields/fld%2Fb',
      '/api/admin/v1/models/mdl%2Fa/fields/fld%2Fb',
      '/api/admin/v1/models/mdl%2Fa/fields/fld%2Froot/children',
      '/api/admin/v1/models/mdl%2Fa/fields/fld%2Fb',
      '/api/admin/v1/models/mdl%2Fa/fields/order',
    ])
    expect(fetchMock.mock.calls[2][1]).toEqual(expect.objectContaining({ method: 'PATCH', body: JSON.stringify({ display_name: '新标题' }) }))
    expect(fetchMock.mock.calls[3][1]).toEqual(expect.objectContaining({ method: 'POST', body: JSON.stringify({ key: 'child', display_name: '子字段', type: 'single_line_text' }) }))
    expect(fetchMock.mock.calls[4][1]).toEqual(expect.objectContaining({ method: 'DELETE' }))
    expect(fetchMock.mock.calls[5][1]).toEqual(expect.objectContaining({ method: 'PUT', body: JSON.stringify({ parent_id: 'fld/root', base_field_ids: ['fld/b', 'fld/c'], field_ids: ['fld/c', 'fld/b'] }) }))
  })

  it('创建和轮换 API Key 禁止客户端缓存完整密钥', async () => {
    authStore.setSession(session)
    const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(Response.json({ key: 'cmsk_secret' }, { status: 201 })))
    vi.stubGlobal('fetch', fetchMock)

    await api.createAPIKey({ name: '官网', model_ids: ['mdl_1'], expires_at: null })
    await api.rotateAPIKey('key/a')

    expect(fetchMock.mock.calls[0][1]).toEqual(expect.objectContaining({ method: 'POST', cache: 'no-store', body: JSON.stringify({ name: '官网', model_ids: ['mdl_1'], expires_at: null }) }))
    expect(fetchMock.mock.calls[1][0]).toBe('/api/admin/v1/api-keys/key%2Fa/rotate')
    expect(fetchMock.mock.calls[1][1]).toEqual(expect.objectContaining({ method: 'POST', cache: 'no-store', body: '{}' }))
  })
})
