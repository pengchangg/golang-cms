import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'

vi.mock('./config', () => ({ ASSETS_ENABLED: false, isAssetsEnabled: () => false }))

import App from './App'
import type { SessionResponse } from './api/types'
import { authStore } from './auth/store'
import { DynamicContentForm } from './components/DynamicContentForm'
import { TransferActions } from './components/TransferActions'

const session: SessionResponse = {
  principal: {
    user_id: 'usr_assets_disabled', display_name: '阶段二用户', email: null, auth_method: 'oidc',
    system_permissions: ['assets.view', 'transfers.execute', 'transfers.download'],
    model_permissions: [{ model_id: 'mdl_1', permissions: ['content.view', 'content.create'] }],
  },
  csrf_token: 'csrf-token-with-at-least-thirty-two-characters',
  idle_expires_at: '2026-07-20T10:00:00Z',
  expires_at: '2026-07-20T20:00:00Z',
}

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  authStore.reset()
  window.history.replaceState({}, '', '/')
})

describe('显式禁用素材能力', () => {
  it('隐藏导航并将素材路由返回首页，不请求不存在的接口', async () => {
    window.history.replaceState({}, '', '/assets')
    const fetchMock = vi.fn().mockResolvedValue(Response.json(session))
    vi.stubGlobal('fetch', fetchMock)
    render(<App />)

    expect(await screen.findByRole('heading', { name: '早上好，阶段二用户' })).toBeInTheDocument()
    expect(screen.queryByText('素材库')).not.toBeInTheDocument()
    expect(screen.queryByText('传输任务')).not.toBeInTheDocument()
    await waitFor(() => expect(window.location.pathname).toBe('/'))
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('不展示媒体选择器和传输操作', () => {
    const mediaField = {
      id: 'fld_cover', key: 'cover', display_name: '封面', description: '', type: 'single_media' as const,
      required: false, default_value: null, constraints: {}, children: [], status: 'active' as const,
      created_at: '2026-07-20T00:00:00Z', updated_at: '2026-07-20T00:00:00Z',
    }
    const { rerender } = render(<DynamicContentForm fields={[mediaField]} content={{ cover: 'ast_1' }} onChange={vi.fn()} />)
    expect(screen.queryByRole('button', { name: '更换素材' })).not.toBeInTheDocument()

    rerender(<MemoryRouter><TransferActions principal={session.principal} modelId="mdl_1" exportQuery={{}} /></MemoryRouter>)
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
    expect(screen.queryByRole('link')).not.toBeInTheDocument()
  })
})
