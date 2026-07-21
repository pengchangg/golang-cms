import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import App from './App'
import { authStore } from './auth/store'
import type { SessionResponse } from './api/types'

const session: SessionResponse = {
  principal: { user_id: 'usr_accessible', display_name: '林岚', email: 'linlan@example.com', auth_method: 'sms', system_permissions: [], model_permissions: [] },
  content_models: [],
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
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(Response.json({ error: { code: 'session_invalid', message: '会话失效', request_id: 'req_login', details: [] } }, { status: 401 }))
      .mockResolvedValueOnce(Response.json({ challenge_id: 'cap_1', background_image: '/captcha-bg', tile_image: '/captcha-piece', tile_x: 80, tile_y: 72, expires_at: '2026-07-18T09:02:00Z' }))
    vi.stubGlobal('fetch', fetchMock)
    render(<App />)

    expect(await screen.findByRole('heading', { name: '回到内容工作的现场' })).toBeInTheDocument()
    expect(screen.getByLabelText('手机号')).toHaveAttribute('autocomplete', 'tel-national')
    expect(await screen.findByLabelText('拖动滑块，使拼图对齐缺口')).toHaveAttribute('type', 'range')
    expect(screen.getByText('本地应急登录')).toBeVisible()
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

  it('内容导航展示模型名称和稳定标识而不是模型 ID', async () => {
    const modelSession: SessionResponse = {
      ...session,
      principal: { ...session.principal, model_permissions: [{ model_id: 'mdl_articles', permissions: ['content.view'] }] },
      content_models: [{ id: 'mdl_articles', key: 'articles', display_name: '内部文章' }],
    }
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(Response.json(modelSession)))
    render(<App />)
    expect(await screen.findByText('内部文章')).toBeVisible()
    expect(screen.getByText('articles')).toBeVisible()
    expect(screen.queryByText(/内容 · mdl_articles/)).not.toBeInTheDocument()
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

  it('短信登录可用键盘调整拼图并返回完整的 pathname、search 和 hash', async () => {
    window.history.replaceState({}, '', '/?tab=drafts#editor')
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(Response.json({ error: { code: 'session_invalid', message: '会话失效', request_id: 'req_login', details: [] } }, { status: 401 }))
      .mockResolvedValueOnce(Response.json({ challenge_id: 'cap_1', background_image: '/captcha-bg', tile_image: '/captcha-piece', tile_x: 80, tile_y: 72, expires_at: '2026-07-18T09:02:00Z' }))
      .mockResolvedValueOnce(Response.json({ challenge_id: 'sms_1', phone_masked: '138****8000', expires_at: '2026-07-18T09:05:00Z', retry_after_seconds: 60 }))
      .mockResolvedValueOnce(Response.json(session))
    vi.stubGlobal('fetch', fetchMock)
    render(<App />)

    await userEvent.type(await screen.findByLabelText('手机号'), '13800138000')
    const slider = await screen.findByLabelText('拖动滑块，使拼图对齐缺口')
    slider.focus()
    await userEvent.keyboard('{ArrowRight}{ArrowRight}')
    await userEvent.click(screen.getByRole('button', { name: '发送验证码' }))
    expect(fetchMock.mock.calls[2][1]).toEqual(expect.objectContaining({ body: JSON.stringify({ phone: '13800138000', captcha_challenge_id: 'cap_1', captcha_x: 2, captcha_y: 72 }) }))
    expect(screen.getByText(/138\*\*\*\*8000/)).toBeVisible()
    await userEvent.type(await screen.findByLabelText('短信验证码'), '123456')
    await userEvent.click(screen.getByRole('button', { name: /^登\s*录$/ }))

    await waitFor(() => expect(window.location.pathname + window.location.search + window.location.hash).toBe('/?tab=drafts#editor'))
  })

  it('短信验证码只接受 6 位数字且重新发送返回新拼图流程', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(Response.json({ error: { code: 'session_invalid', message: '会话失效', request_id: 'req_login', details: [] } }, { status: 401 }))
      .mockResolvedValueOnce(Response.json({ challenge_id: 'cap_1', background_image: '/captcha-bg', tile_image: '/captcha-piece', tile_x: 80, tile_y: 72, expires_at: '2026-07-18T09:02:00Z' }))
      .mockResolvedValueOnce(Response.json({ challenge_id: 'sms_1', phone_masked: '138****8000', expires_at: '2026-07-18T09:05:00Z', retry_after_seconds: 0 }))
      .mockResolvedValueOnce(Response.json({ challenge_id: 'cap_2', background_image: '/captcha-bg-2', tile_image: '/captcha-piece-2', tile_x: 120, tile_y: 84, expires_at: '2026-07-18T09:04:00Z' }))
    vi.stubGlobal('fetch', fetchMock)
    render(<App />)

    await userEvent.type(await screen.findByLabelText('手机号'), '13800138000')
    await userEvent.click(screen.getByRole('button', { name: '发送验证码' }))
    const code = await screen.findByLabelText('短信验证码')
    expect(code).toHaveAttribute('maxlength', '6')
    await userEvent.type(code, '12345a')
    await userEvent.click(screen.getByRole('button', { name: /^登\s*录$/ }))
    expect(await screen.findByText('请输入 6 位数字验证码')).toBeVisible()
    expect(fetchMock).toHaveBeenCalledTimes(3)

    await userEvent.click(screen.getByRole('button', { name: '重新发送验证码' }))
    expect(await screen.findByLabelText('拖动滑块，使拼图对齐缺口')).toBeVisible()
    expect(fetchMock).toHaveBeenCalledTimes(4)
  })

})
