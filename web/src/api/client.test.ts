import { afterEach, describe, expect, it, vi } from 'vitest'

import { authStore } from '../auth/store'
import { ApiError, api, oidcStartUrl, safeReturnTo } from './client'
import type { SessionResponse } from './types'

const session: SessionResponse = {
  principal: { user_id: 'usr_test', display_name: '测试用户', email: null, auth_method: 'local', system_permissions: [], model_permissions: [] },
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
})
