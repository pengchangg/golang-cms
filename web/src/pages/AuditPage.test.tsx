import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '../api/client'
import type { AuditEvent, Principal } from '../api/types'
import AuditPage from './AuditPage'

afterEach(() => { cleanup(); vi.restoreAllMocks() })

it('审计日志使用 next_cursor 请求下一页并支持返回', async () => {
  const event: AuditEvent = { id: 'evt_1', actor_type: 'system', actor_id: null, actor_display_name: null, action: 'session_created', resource_type: 'session', resource_id: null, request_id: 'req_1', ip: '127.0.0.1', user_agent: 'test', result: 'success', failure_code: null, changes: {}, occurred_at: '2026-07-22T00:00:00Z' }
  const list = vi.spyOn(api, 'listAuditEvents')
    .mockResolvedValueOnce({ items: [event], next_cursor: 'audit_2' })
    .mockResolvedValueOnce({ items: [], next_cursor: null })
    .mockResolvedValueOnce({ items: [event], next_cursor: 'audit_2' })
  const principal: Principal = { user_id: 'usr_1', display_name: '管理员', email: null, auth_method: 'local', is_emergency_admin: false, has_high_risk_role: false, system_permissions: ['audit.view'], model_permissions: [] }
  render(<MemoryRouter><AuditPage principal={principal} /></MemoryRouter>)
  await userEvent.click(await screen.findByRole('button', { name: '下一页' }))
  await waitFor(() => expect(list).toHaveBeenLastCalledWith({ action: '', result: undefined, cursor: 'audit_2' }))
  await userEvent.click(screen.getByRole('button', { name: '上一页' }))
  await waitFor(() => expect(list).toHaveBeenLastCalledWith({ action: '', result: undefined, cursor: undefined }))
})
