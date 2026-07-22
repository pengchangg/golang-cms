import { afterEach, describe, expect, it, vi } from 'vitest'

import { authStore } from '../auth/store'
import { contentAPICurl, requestContentAPI } from './contentExplorer'
import type { SessionResponse } from './types'

const apiKey = `cmsk_abcdefghijkl_${'x'.repeat(43)}`
const session: SessionResponse = {
  principal: { user_id: 'usr_1', display_name: '调试用户', email: null, auth_method: 'local', is_emergency_admin: false, has_high_risk_role: false, system_permissions: ['api_keys.view'], model_permissions: [] },
  content_models: [],
  csrf_token: 'csrf-token-with-at-least-thirty-two-characters',
  idle_expires_at: '2026-07-22T10:00:00Z',
  expires_at: '2026-07-22T20:00:00Z',
}

afterEach(() => {
  vi.unstubAllGlobals()
  authStore.reset()
})

describe('Content API 调试请求层', () => {
  it('仅发送同源 GET 并返回原始响应信息', async () => {
    vi.spyOn(performance, 'now').mockReturnValueOnce(10).mockReturnValueOnce(26.4)
    const fetchMock = vi.fn().mockResolvedValue(Response.json({ items: [] }, { headers: { ETag: '"sha256-test"', 'X-Request-ID': 'req_content' } }))
    vi.stubGlobal('fetch', fetchMock)

    const response = await requestContentAPI('/models/articles/entries?limit=7', apiKey, { ifNoneMatch: '"sha256-old"' })

    expect(fetchMock).toHaveBeenCalledWith('/api/content/v1/models/articles/entries?limit=7', expect.objectContaining({ method: 'GET', credentials: 'same-origin', cache: 'no-store' }))
    const headers = fetchMock.mock.calls[0][1].headers as Headers
    expect(headers.get('Authorization')).toBe(`Bearer ${apiKey}`)
    expect(headers.get('If-None-Match')).toBe('"sha256-old"')
    expect(response).toMatchObject({ status: 200, durationMs: 16.4, data: { items: [] }, headers: { etag: '"sha256-test"', 'x-request-id': 'req_content' } })
  })

  it.each(['/assets/ast_1', '/models/articles/drafts', '//outside.example/models', '/models\\articles'])('拒绝白名单外路径 %s', async (path) => {
    vi.stubGlobal('fetch', vi.fn())
    await expect(requestContentAPI(path, apiKey)).rejects.toThrow()
    expect(fetch).not.toHaveBeenCalled()
  })

  it('Content API 401 不清除管理会话', async () => {
    authStore.setSession(session)
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(Response.json({ error: { code: 'invalid_api_key', message: 'Key 无效', request_id: 'req_401', details: [] } }, { status: 401 })))

    await expect(requestContentAPI('/models', apiKey)).resolves.toMatchObject({ status: 401, data: { error: { code: 'invalid_api_key' } } })
    expect(authStore.getSnapshot()).toMatchObject({ status: 'authenticated', session })
  })

  it('cURL 只复制环境变量占位符', () => {
    const command = contentAPICurl('/models/articles?filter=$(unsafe)', '"etag"')
    expect(command.split('\n')).toEqual([
      'curl --fail-with-body \\',
      '  --header "Authorization: Bearer $API_KEY" \\',
      '  --header \'If-None-Match: "etag"\' \\',
      '  "$BASE_URL/api/content/v1/models/articles?filter=%24(unsafe)"',
    ])
    expect(command).toContain('Bearer $API_KEY')
    expect(command).toContain('$BASE_URL/api/content/v1/models/articles?filter=%24(unsafe)')
    expect(command).not.toContain(apiKey)
  })
})
