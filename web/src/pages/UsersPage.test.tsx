import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '../api/client'
import type { Principal, User, UserSummary } from '../api/types'
import UsersPage from './UsersPage'

const now = '2026-07-21T08:00:00Z'
const principal: Principal = {
  user_id: 'usr_admin', display_name: '管理员', email: null, auth_method: 'local',
  system_permissions: ['users.view', 'users.manage', 'roles.view', 'roles.manage'], model_permissions: [],
}
const summary: UserSummary = {
  id: 'usr_1', display_name: '林岚', email: null, phone_masked: '138****8000', auth_methods: ['sms'],
  is_emergency_admin: false, status: 'enabled', created_at: now, updated_at: now,
}
const detail: User = { ...summary, phone: '13800138000', role_ids: [] }

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('用户管理', () => {
  it('打开手机号编辑前加载详情并显示完整当前手机号', async () => {
    vi.spyOn(api, 'listUsers').mockResolvedValue({ items: [summary], next_cursor: null })
    vi.spyOn(api, 'listRoles').mockResolvedValue({ items: [] })
    const getUser = vi.spyOn(api, 'getUser').mockResolvedValue(detail)

    render(<UsersPage principal={principal} />)
    await userEvent.click(await screen.findByRole('button', { name: '编辑手机号' }))

    await waitFor(() => expect(getUser).toHaveBeenCalledWith('usr_1'))
    await waitFor(() => expect(screen.getAllByText('当前手机号：13800138000').some((element) => element.getAttribute('aria-hidden') !== 'true' && !element.closest('[aria-hidden="true"]'))).toBe(true))
  })

  it('创建手机号账户时允许不分配角色', async () => {
    vi.spyOn(api, 'listUsers').mockResolvedValue({ items: [summary], next_cursor: null })
    vi.spyOn(api, 'listRoles').mockResolvedValue({ items: [] })
    const createUser = vi.spyOn(api, 'createUser').mockResolvedValue(detail)

    render(<UsersPage principal={principal} />)
    await userEvent.click(await screen.findByRole('button', { name: '创建手机号账户' }))
    await userEvent.type(screen.getByLabelText('显示名称'), '新用户')
    await userEvent.type(screen.getByLabelText('手机号'), '13900139000')
    await userEvent.click(screen.getByRole('button', { name: /^创\s*建$/ }))

    await waitFor(() => expect(createUser).toHaveBeenCalledWith({ display_name: '新用户', phone: '13900139000', role_ids: [] }))
  })
})
