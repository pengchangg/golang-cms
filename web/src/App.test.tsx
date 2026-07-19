import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import App from './App'
import { authStore } from './auth/store'
import type { SessionResponse } from './api/types'

const session: SessionResponse = {
  principal: { user_id: 'usr_accessible', display_name: '林岚', email: 'linlan@example.com', auth_method: 'oidc', system_permissions: [], model_permissions: [] },
  csrf_token: 'csrf-token-with-at-least-thirty-two-characters',
  idle_expires_at: '2026-07-18T10:00:00Z',
  expires_at: '2026-07-18T20:00:00Z',
}

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  authStore.reset()
  window.history.replaceState({}, '', '/')
})

describe('认证界面', () => {
  it('未认证时展示可访问的登录表单和明确标签', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(Response.json({ error: { code: 'session_invalid', message: '会话失效', request_id: 'req_login', details: [] } }, { status: 401 })))
    render(<App />)

    expect(await screen.findByRole('heading', { name: '回到内容工作的现场' })).toBeInTheDocument()
    expect(screen.getByLabelText('管理员账号')).toHaveAttribute('autocomplete', 'username')
    expect(screen.getByLabelText('密码')).toHaveAttribute('autocomplete', 'current-password')
    expect(screen.getByRole('link', { name: '使用企业 SSO 登录' })).toBeVisible()
  })

  it('认证后呈现当前用户、跳转链接和响应式导航按钮', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(Response.json(session)))
    render(<App />)

    expect(await screen.findByRole('heading', { name: '早上好，林岚' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '跳到主要内容' })).toHaveAttribute('href', '#main-content')
    const trigger = screen.getByRole('button', { name: '打开导航' })
    await userEvent.click(trigger)
    await waitFor(() => expect(screen.getByRole('navigation', { name: '移动端主导航' })).toBeVisible())
  })

  it.each([
    ['403', () => Response.json({ error: { code: 'permission_denied', message: '无权访问', request_id: 'req_403', details: [] } }, { status: 403 })],
    ['5xx', () => Response.json({ error: { code: 'service_unavailable', message: '服务暂不可用', request_id: 'req_503', details: [] } }, { status: 503 })],
    ['网络故障', () => Promise.reject(new TypeError('Failed to fetch'))],
  ])('会话探测遇到%s时进入可重试错误状态而非登录页', async (_name, failure) => {
    const fetchMock = vi.fn()
      .mockImplementationOnce(failure)
      .mockResolvedValueOnce(Response.json(session))
    vi.stubGlobal('fetch', fetchMock)
    render(<App />)

    expect(await screen.findByText('暂时无法确认登录状态')).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: '回到内容工作的现场' })).not.toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: /重\s*试/ }))
    expect(await screen.findByRole('heading', { name: '早上好，林岚' })).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('本地登录后返回完整的 pathname、search 和 hash', async () => {
    window.history.replaceState({}, '', '/?tab=drafts#editor')
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(Response.json({ error: { code: 'session_invalid', message: '会话失效', request_id: 'req_login', details: [] } }, { status: 401 }))
      .mockResolvedValueOnce(Response.json(session))
    vi.stubGlobal('fetch', fetchMock)
    render(<App />)

    await userEvent.type(await screen.findByLabelText('管理员账号'), 'admin')
    expect(screen.getByRole('link', { name: '使用企业 SSO 登录' })).toHaveAttribute(
      'href',
      '/api/admin/v1/auth/oidc/start?return_to=%2F%3Ftab%3Ddrafts%23editor',
    )
    await userEvent.type(screen.getByLabelText('密码'), 'secret')
    await userEvent.click(screen.getByRole('button', { name: '本地应急登录' }))

    await waitFor(() => expect(window.location.pathname + window.location.search + window.location.hash).toBe('/?tab=drafts#editor'))
  })

  it('旧 OIDC 回调只解释失败且不展示外部错误描述', async () => {
    window.history.replaceState({}, '', '/auth/oidc/callback?error=access_denied&error_description=敏感内部信息')
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(Response.json({ error: { code: 'session_invalid', message: '会话失效', request_id: 'req_login', details: [] } }, { status: 401 })))
    render(<App />)

    expect(await screen.findByText('企业身份登录未完成')).toBeInTheDocument()
    expect(screen.queryByText('敏感内部信息')).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('link', { name: '返回登录' }))
    expect(await screen.findByText('企业身份登录未完成，请重新尝试或使用本地应急登录。')).toBeInTheDocument()
  })
})
