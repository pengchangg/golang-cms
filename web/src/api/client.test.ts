import { afterEach, describe, expect, it, vi } from 'vitest'

import { authStore } from '../auth/store'
import { ApiError, api, oidcStartUrl, safeReturnTo, uploadAssetFile } from './client'
import type { SessionResponse } from './types'

const session: SessionResponse = {
  principal: { user_id: 'usr_test', display_name: '测试用户', email: null, auth_method: 'local', system_permissions: [], model_permissions: [] },
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

  it('素材上传仅在 OSS PUT 成功后确认并返回可用素材', async () => {
    const file = new File([new Uint8Array([1, 2, 3])], 'cover.png', { type: 'image/png' })
    Object.defineProperty(file, 'arrayBuffer', { value: async () => new Uint8Array([1, 2, 3]).buffer })
    const asset = { id: 'ast_1', filename: file.name, mime_type: file.type, size: file.size, sha256: 'a'.repeat(64), etag: 'etag', status: 'available' as const, created_by: 'usr_1', created_at: '2026-07-20T08:00:00Z', confirmed_at: '2026-07-20T08:00:00Z', archived_at: null }
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
    const quarantined = { id: 'ast_1', filename: file.name, mime_type: file.type, size: file.size, sha256: 'a'.repeat(64), etag: null, status: 'quarantined' as const, created_by: 'usr_1', created_at: '2026-07-20T08:00:00Z', confirmed_at: null, archived_at: null }
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

  it('403 只作为授权错误，不清空有效认证状态', async () => {
    authStore.setSession(session)
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(Response.json({ error: { code: 'permission_denied', message: '无权操作', request_id: 'req_403', details: [] } }, { status: 403 })))

    await expect(api.logout()).rejects.toMatchObject({ status: 403, code: 'permission_denied' })
    expect(authStore.getSnapshot().status).toBe('authenticated')
  })

  it('只生成同源 OIDC 发起地址', () => {
    expect(oidcStartUrl('/articles?status=draft#editor')).toContain('return_to=%2Farticles%3Fstatus%3Ddraft%23editor')
    expect(() => oidcStartUrl('//outside.example')).toThrow(TypeError)
    expect(() => oidcStartUrl('/\\outside.example')).toThrow(TypeError)
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
