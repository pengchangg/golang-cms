import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '../api/client'
import type { AuditEvent, Principal } from '../api/types'
import AuditPage from './AuditPage'

const principal: Principal = {
  user_id: 'usr_admin', display_name: '管理员', email: null, auth_method: 'local',
  system_permissions: ['audit.view'],
  model_permissions: [{ model_id: 'mdl_articles', permissions: ['content.view'] }],
}

const event: AuditEvent = {
  id: 'evt_1', occurred_at: '2026-07-20T08:00:00Z', request_id: 'req_1',
  actor_type: 'user', actor_id: 'usr_editor', actor_display_name: '内容编辑张三',
  action: 'content_revision_approved', resource_type: 'content_revision', resource_id: 'rev_1',
  result: 'success', ip: '127.0.0.1', user_agent: 'test',
  changes: { model_id: 'mdl_articles', entry_id: 'ent_1', from: 'pending_review', to: 'published' }, failure_code: null,
}

afterEach(() => { cleanup(); vi.restoreAllMocks() })

describe('审计日志可读展示', () => {
  it('显示操作者名称、中文动作并链接到内容资源', async () => {
    vi.spyOn(api, 'listAuditEvents').mockResolvedValue({ items: [event], next_cursor: null })
    render(<MemoryRouter><AuditPage principal={principal} /></MemoryRouter>)
    expect(await screen.findByText('内容编辑张三')).toBeVisible()
    expect(screen.getByText('审核通过并发布')).toBeVisible()
    expect(screen.getByRole('link', { name: /内容版本/ })).toHaveAttribute('href', '/content/mdl_articles/ent_1')
  })

  it('点击资源链接不会同时打开事件详情', async () => {
    vi.spyOn(api, 'listAuditEvents').mockResolvedValue({ items: [event], next_cursor: null })
    render(<MemoryRouter><AuditPage principal={principal} /></MemoryRouter>)
    const link = await screen.findByRole('link', { name: /内容版本/ })
    await userEvent.click(link)
    expect(screen.queryByText('事件详情')).not.toBeInTheDocument()
  })
})
