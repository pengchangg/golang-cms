import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '../api/client'
import type { APIKey, Principal, Role, SystemPermission } from '../api/types'
import APIKeysPage from './APIKeysPage'
import RolesPage from './RolesPage'

const now = '2026-07-22T00:00:00Z'
const role: Role = { id: 'rol_1', key: 'reader', kind: 'custom', display_name: '只读角色', description: '', system_permissions: [], model_permissions: [{ model_id: 'mdl_1', permissions: ['content.view'] }], config_namespace_permissions: [], created_at: now, updated_at: now }
const key: APIKey = { id: 'key_1', name: '客户端', prefix: 'prefix', model_ids: ['mdl_1'], status: 'active', expires_at: null, revoked_at: null, last_used_at: null, rotated_from_id: null, replaced_by_id: null, created_by: 'usr_1', created_at: now }
const principal = (permissions: SystemPermission[], readableModels: string[] = []): Principal => ({ user_id: 'usr_1', display_name: '管理员', email: null, auth_method: 'local', is_emergency_admin: false, has_high_risk_role: false, system_permissions: permissions, model_permissions: readableModels.map((model_id) => ({ model_id, permissions: ['content.view'] })), config_namespace_permissions: [] })

afterEach(() => { cleanup(); vi.restoreAllMocks() })

it('只有 roles.view 时不请求模型目录并保留已有模型授权只读展示', async () => {
  vi.spyOn(api, 'listRoles').mockResolvedValue({ items: [role] })
  const listModels = vi.spyOn(api, 'listModels')
  render(<RolesPage principal={principal(['roles.view'])} />)
  const roleItem = await screen.findByRole('button', { name: /只读角色/ })
  roleItem.focus()
  await userEvent.keyboard('{Enter}')
  expect(await screen.findByText('mdl_1')).toBeVisible()
  expect(screen.getByRole('button', { name: '新建角色' })).toBeDisabled()
  expect(screen.getByRole('button', { name: '保存系统权限' })).toBeDisabled()
  expect(listModels).not.toHaveBeenCalled()
})

it('roles.manage 单独开放角色写操作', async () => {
  const listRoles = vi.spyOn(api, 'listRoles')
  const listModels = vi.spyOn(api, 'listModels')
  const createRole = vi.spyOn(api, 'createRole').mockResolvedValue(role)
  render(<RolesPage principal={principal(['roles.manage'])} />)
  await userEvent.click(await screen.findByRole('button', { name: '新建角色' }))
  await userEvent.type(screen.getByLabelText('稳定标识'), 'reader')
  await userEvent.type(screen.getByLabelText('显示名称'), '只读角色')
  await userEvent.click(screen.getByRole('button', { name: 'OK' }))
  await waitFor(() => expect(createRole).toHaveBeenCalledWith({ key: 'reader', display_name: '只读角色' }))
  expect(screen.getByText('当前账号可新建角色，但没有角色列表查看权限。')).toBeVisible()
  expect(listRoles).not.toHaveBeenCalled()
  expect(listModels).not.toHaveBeenCalled()
})

it('展示角色已有的归档命名空间授权且只允许清空后保存', async () => {
  const roleWithArchivedGrant: Role = {
    ...role,
    config_namespace_permissions: [{ config_namespace_id: 'cfg_archived', permissions: ['config.view'] }],
  }
  vi.spyOn(api, 'listRoles').mockResolvedValue({ items: [roleWithArchivedGrant] })
  vi.spyOn(api, 'listConfigurationNamespaces').mockResolvedValue({ items: [{ id: 'cfg_active', namespace_key: 'site', display_name: '站点配置', description: '', status: 'active', created_at: now, updated_at: now }] })
  const updatePermissions = vi.spyOn(api, 'updateRoleConfigNamespacePermissions').mockResolvedValue({ ...roleWithArchivedGrant, config_namespace_permissions: [] })

  render(<RolesPage principal={principal(['roles.view', 'roles.manage', 'configurations.view'])} />)
  await userEvent.click(await screen.findByRole('button', { name: /只读角色/ }))

  const activeGrant = screen.getByText('站点配置').closest('section')!
  expect(within(activeGrant).getByRole('checkbox', { name: 'config.create' })).toBeEnabled()
  const archivedGrant = screen.getByText('cfg_archived').closest('section')!
  expect(within(archivedGrant).getByText('已归档或不可见')).toBeVisible()
  expect(within(archivedGrant).getByRole('checkbox', { name: 'config.view' })).toBeEnabled()
  expect(within(archivedGrant).getByRole('checkbox', { name: 'config.create' })).toBeDisabled()

  await userEvent.click(within(archivedGrant).getByRole('checkbox', { name: 'config.view' }))
  expect(screen.queryByText('cfg_archived')).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: '保存配置权限' }))
  await waitFor(() => expect(updatePermissions).toHaveBeenCalledWith('rol_1', []))
})

it('没有 configurations.view 时仍可清理角色已有的不可见命名空间授权', async () => {
  const roleWithInvisibleGrant: Role = {
    ...role,
    config_namespace_permissions: [{ config_namespace_id: 'cfg_invisible', permissions: ['config.update'] }],
  }
  vi.spyOn(api, 'listRoles').mockResolvedValue({ items: [roleWithInvisibleGrant] })
  const listNamespaces = vi.spyOn(api, 'listConfigurationNamespaces')
  const updatePermissions = vi.spyOn(api, 'updateRoleConfigNamespacePermissions').mockResolvedValue({ ...roleWithInvisibleGrant, config_namespace_permissions: [] })

  render(<RolesPage principal={principal(['roles.view', 'roles.manage'])} />)
  await userEvent.click(await screen.findByRole('button', { name: /只读角色/ }))

  const invisibleGrant = screen.getByText('cfg_invisible').closest('section')!
  await userEvent.click(within(invisibleGrant).getByRole('checkbox', { name: 'config.update' }))
  await userEvent.click(screen.getByRole('button', { name: '保存配置权限' }))

  await waitFor(() => expect(updatePermissions).toHaveBeenCalledWith('rol_1', []))
  expect(listNamespaces).not.toHaveBeenCalled()
})

it('只有 api_keys.create 时可录入模型 ID 创建', async () => {
  const listKeys = vi.spyOn(api, 'listAPIKeys')
  const listModels = vi.spyOn(api, 'listModels')
  const createKey = vi.spyOn(api, 'createAPIKey').mockResolvedValue({ ...key, key: 'cmsk_secret' })
  render(<APIKeysPage principal={principal(['api_keys.create'], ['mdl_1'])} />)
  await userEvent.click(await screen.findByRole('button', { name: /创建 API Key/ }))
  await userEvent.type(screen.getByLabelText('名称'), '新客户端')
  await userEvent.click(screen.getByRole('combobox', { name: '模型范围' }))
  const options = await screen.findAllByText('mdl_1')
  await userEvent.click(options.find((element) => element.classList.contains('ant-select-item-option-content'))!)
  await userEvent.click(screen.getByRole('button', { name: /^创\s*建$/ }))
  await waitFor(() => expect(createKey).toHaveBeenCalledWith({ name: '新客户端', model_ids: ['mdl_1'], config_namespace_ids: [], expires_at: null }))
  expect(listKeys).not.toHaveBeenCalled()
  expect(listModels).not.toHaveBeenCalled()
}, 20_000)

it('密钥弹窗复制完整 Key 字符串而不是 [object Object]', async () => {
  const secretKey = 'cmsk_huwjnv452ymh_44hsEdNk-JhG5iJwdxyYS9T2rxiY8RAUFm3J3jxXjTw'
  const writeText = vi.fn().mockResolvedValue(undefined)
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
  const createKey = vi.spyOn(api, 'createAPIKey').mockResolvedValue({ ...key, key: secretKey })
  render(<APIKeysPage principal={principal(['api_keys.create'], ['mdl_1'])} />)
  await userEvent.click(await screen.findByRole('button', { name: /创建 API Key/ }))
  await userEvent.type(screen.getByLabelText('名称'), '新客户端')
  await userEvent.click(screen.getByRole('combobox', { name: '模型范围' }))
  const options = await screen.findAllByText('mdl_1')
  await userEvent.click(options.find((element) => element.classList.contains('ant-select-item-option-content'))!)
  await userEvent.click(screen.getByRole('button', { name: /^创\s*建$/ }))
  await waitFor(() => expect(createKey).toHaveBeenCalled())
  const copyButton = await screen.findByRole('button', { name: '复制完整 Key' })
  await userEvent.click(copyButton)
  await waitFor(() => expect(writeText).toHaveBeenCalledWith(secretKey))
  expect(writeText).not.toHaveBeenCalledWith('[object Object]')
}, 20_000)

it('只有 api_keys.create 时可按已知 ID 轮换', async () => {
  const listKeys = vi.spyOn(api, 'listAPIKeys')
  const rotateKey = vi.spyOn(api, 'rotateAPIKey').mockResolvedValue({ ...key, key: 'cmsk_rotated' })
  render(<APIKeysPage principal={principal(['api_keys.create'], ['mdl_1'])} />)
  await userEvent.click(screen.getByRole('button', { name: /按 ID 轮换/ }))
  await userEvent.type(screen.getByLabelText('API Key ID'), 'key_known')
  await userEvent.click(screen.getByRole('button', { name: /^轮\s*换$/ }))
  await waitFor(() => expect(rotateKey).toHaveBeenCalledWith('key_known'))
  expect(listKeys).not.toHaveBeenCalled()
})

it('只有 api_keys.revoke 时可按 ID 撤销且不请求列表', async () => {
  const listKeys = vi.spyOn(api, 'listAPIKeys')
  const revokeKey = vi.spyOn(api, 'revokeAPIKey').mockResolvedValue(undefined)
  render(<APIKeysPage principal={principal(['api_keys.revoke'])} />)
  await userEvent.click(await screen.findByRole('button', { name: /按 ID 撤销/ }))
  await userEvent.type(screen.getByLabelText('API Key ID'), 'key_known')
  await userEvent.click(screen.getByRole('button', { name: /^撤\s*销$/ }))
  await waitFor(() => expect(revokeKey).toHaveBeenCalledWith('key_known'))
  expect(listKeys).not.toHaveBeenCalled()
})

it('API Key 查看、创建和撤销权限分别控制操作且不要求 models.view', async () => {
  vi.spyOn(api, 'listAPIKeys').mockResolvedValue({ items: [key], next_cursor: null })
  const listModels = vi.spyOn(api, 'listModels')
  const { rerender } = render(<APIKeysPage principal={principal(['api_keys.view'])} />)
  expect(await screen.findByRole('button', { name: /创建 API Key/ })).toBeDisabled()
  expect(screen.getByRole('button', { name: /轮\s*换/ })).toBeDisabled()
  expect(screen.getByRole('button', { name: /撤\s*销/ })).toBeDisabled()
  expect(listModels).not.toHaveBeenCalled()

  rerender(<APIKeysPage principal={principal(['api_keys.view', 'api_keys.create'], ['mdl_1'])} />)
  expect(await screen.findByRole('button', { name: /创建 API Key/ })).toBeEnabled()
  expect(screen.getByRole('button', { name: /轮\s*换/ })).toBeEnabled()
  expect(screen.getByRole('button', { name: /撤\s*销/ })).toBeDisabled()

  rerender(<APIKeysPage principal={principal(['api_keys.view', 'api_keys.revoke'])} />)
  expect(screen.getByRole('button', { name: /创建 API Key/ })).toBeDisabled()
  expect(screen.getByRole('button', { name: /轮\s*换/ })).toBeDisabled()
  expect(screen.getByRole('button', { name: /撤\s*销/ })).toBeEnabled()
})
