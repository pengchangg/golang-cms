import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '../api/client'
import type { Principal, User, UserSummary } from '../api/types'
import UsersPage from './UsersPage'

const now = '2026-07-21T08:00:00Z'
const principal: Principal = {
  user_id: 'usr_admin', display_name: '管理员', email: null, auth_method: 'local',
  is_emergency_admin: false, has_high_risk_role: false,
  system_permissions: ['users.view', 'users.manage', 'roles.view', 'roles.manage'], model_permissions: [],
}
const summary: UserSummary = {
  id: 'usr_1', display_name: '林岚', email: null, phone_masked: '138****8000', auth_methods: ['sms'],
  is_emergency_admin: false, has_high_risk_role: false, status: 'enabled', created_at: now, updated_at: now,
}
const detail: User = { ...summary, phone: '13800138000', role_ids: [] }

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('用户管理', () => {
  it('打开手机号编辑前加载详情并只显示脱敏当前手机号', async () => {
    vi.spyOn(api, 'listUsers').mockResolvedValue({ items: [summary], next_cursor: null })
    vi.spyOn(api, 'listRoles').mockResolvedValue({ items: [] })
    const getUser = vi.spyOn(api, 'getUser').mockResolvedValue(detail)

    render(<UsersPage principal={{ ...principal, has_high_risk_role: true }} />)
    await userEvent.click(await screen.findByRole('button', { name: '编辑手机号' }))

    await waitFor(() => expect(getUser).toHaveBeenCalledWith('usr_1'))
    await waitFor(() => expect(screen.getAllByText(/当前手机号：138\*\*\*\*8000。保存后该用户的现有会话将全部撤销。/).some((element) => element.getAttribute('aria-hidden') !== 'true' && !element.closest('[aria-hidden="true"]'))).toBe(true))
  })

  it('普通管理员不能编辑已激活手机号', async () => {
    vi.spyOn(api, 'listUsers').mockResolvedValue({ items: [summary], next_cursor: null })
    vi.spyOn(api, 'listRoles').mockResolvedValue({ items: [] })

    render(<UsersPage principal={principal} />)

    expect(await screen.findByRole('button', { name: '编辑手机号' })).toBeDisabled()
  })

  it('应急管理员目标的手机号入口始终禁用', async () => {
    vi.spyOn(api, 'listUsers').mockResolvedValue({ items: [{ ...summary, is_emergency_admin: true }], next_cursor: null })
    vi.spyOn(api, 'listRoles').mockResolvedValue({ items: [] })

    render(<UsersPage principal={{ ...principal, is_emergency_admin: true }} />)

    expect(await screen.findByRole('button', { name: '编辑手机号' })).toBeDisabled()
  })

  it('高危角色不能管理应急管理员状态或角色', async () => {
    vi.spyOn(api, 'listUsers').mockResolvedValue({ items: [{ ...summary, is_emergency_admin: true }], next_cursor: null })
    vi.spyOn(api, 'listRoles').mockResolvedValue({ items: [] })

    render(<UsersPage principal={{ ...principal, has_high_risk_role: true }} />)

    expect(await screen.findByRole('switch')).toBeDisabled()
    expect(screen.getByRole('button', { name: '管理角色' })).toBeDisabled()
  })

  it('应急管理员可以管理其他应急管理员状态和角色', async () => {
    vi.spyOn(api, 'listUsers').mockResolvedValue({ items: [{ ...summary, is_emergency_admin: true }], next_cursor: null })
    vi.spyOn(api, 'listRoles').mockResolvedValue({ items: [] })

    render(<UsersPage principal={{ ...principal, is_emergency_admin: true }} />)

    expect(await screen.findByRole('switch')).toBeEnabled()
    expect(screen.getByRole('button', { name: '管理角色' })).toBeEnabled()
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

  it('使用 next_cursor 请求下一页并支持返回上一页', async () => {
	const listUsers = vi.spyOn(api, 'listUsers')
	  .mockResolvedValueOnce({ items: [summary], next_cursor: 'users_2' })
	  .mockResolvedValueOnce({ items: [], next_cursor: null })
	  .mockResolvedValueOnce({ items: [summary], next_cursor: 'users_2' })
	vi.spyOn(api, 'listRoles').mockResolvedValue({ items: [] })
	render(<UsersPage principal={principal} />)
	await userEvent.click(await screen.findByRole('button', { name: '下一页' }))
	await waitFor(() => expect(listUsers).toHaveBeenLastCalledWith({ query: '', status: undefined, cursor: 'users_2' }))
	await userEvent.click(screen.getByRole('button', { name: '上一页' }))
	await waitFor(() => expect(listUsers).toHaveBeenLastCalledWith({ query: '', status: undefined, cursor: undefined }))
  })

  it('下一页请求在途时重复点击不会重复压入游标', async () => {
    let resolveNext: ((value: { items: UserSummary[]; next_cursor: null }) => void) | undefined
    vi.spyOn(api, 'listUsers')
      .mockResolvedValueOnce({ items: [summary], next_cursor: 'users_2' })
      .mockImplementationOnce(() => new Promise((resolve) => { resolveNext = resolve }))
    vi.spyOn(api, 'listRoles').mockResolvedValue({ items: [] })
    render(<UsersPage principal={principal} />)
    const next = await screen.findByRole('button', { name: '下一页' })
    await userEvent.dblClick(next)
    expect(screen.getByRole('button', { name: '上一页' })).toBeEnabled()
    await userEvent.click(screen.getByRole('button', { name: '上一页' }))
    expect(screen.getByRole('button', { name: '上一页' })).toBeDisabled()
    resolveNext?.({ items: [], next_cursor: null })
  })

  it('筛选切换后新请求完成前不暴露旧游标', async () => {
    vi.spyOn(api, 'listUsers')
      .mockResolvedValueOnce({ items: [summary], next_cursor: 'users_2' })
      .mockImplementationOnce(() => new Promise(() => {}))
    vi.spyOn(api, 'listRoles').mockResolvedValue({ items: [] })
    render(<UsersPage principal={principal} />)
    expect(await screen.findByRole('button', { name: '下一页' })).toBeEnabled()
    await userEvent.type(screen.getByRole('searchbox', { name: '搜索用户' }), '新条件{enter}')
    expect(screen.getByRole('button', { name: '下一页' })).toBeDisabled()
  })

  it('重复提交相同搜索条件也会重新请求', async () => {
    const listUsers = vi.spyOn(api, 'listUsers').mockResolvedValue({ items: [summary], next_cursor: null })
    vi.spyOn(api, 'listRoles').mockResolvedValue({ items: [] })
    render(<UsersPage principal={principal} />)
    const search = await screen.findByRole('searchbox', { name: '搜索用户' })
    await userEvent.type(search, '同一条件{enter}')
    await waitFor(() => expect(listUsers).toHaveBeenCalledTimes(2))
    await userEvent.clear(search)
    await userEvent.type(search, '同一条件{enter}')
    await waitFor(() => expect(listUsers).toHaveBeenCalledTimes(3))
  })
})
