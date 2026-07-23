import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '../api/client'
import type { ConfigurationItemValue, ConfigurationNamespace, Principal } from '../api/types'
import ConfigurationItemPage from './ConfigurationItemPage'
import ConfigurationsPage from './ConfigurationsPage'

const now = '2026-07-23T00:00:00Z'
const namespace: ConfigurationNamespace = { id: 'cns_11111111111111111111111111111111', namespace_key: 'site', display_name: '站点配置', description: '公共配置', status: 'active', created_at: now, updated_at: now }
const principal: Principal = { user_id: 'usr_1', display_name: '管理员', email: null, auth_method: 'local', is_emergency_admin: false, has_high_risk_role: false, system_permissions: ['configurations.view', 'configurations.create', 'configurations.update', 'configurations.archive'], model_permissions: [], config_namespace_permissions: [{ config_namespace_id: namespace.id, permissions: ['config.view', 'config.create', 'config.update', 'config.submit'] }] }
const itemValue: ConfigurationItemValue = {
  item: { id: 'cit_1', namespace_id: namespace.id, item_key: 'support_phone', display_name: '客服电话', description: '', value_type: 'string', constraints: { required: true }, status: 'active', created_by: 'usr_1', created_at: now, updated_at: now },
  current_draft_revision: { id: 'crv_1', item_id: 'cit_1', namespace_id: namespace.id, revision_number: 1, value_type: 'string', constraints: { required: true }, value: '400-0000', workflow_status: 'draft', created_by: 'usr_1', submitted_by: null, submitted_at: null, created_at: now },
  current_published_revision_id: null,
}

afterEach(() => { cleanup(); vi.restoreAllMocks() })

function mockConfigurationHistory() {
  vi.spyOn(api, 'listConfigurationRevisions').mockResolvedValue({ items: [], next_cursor: null })
  vi.spyOn(api, 'listConfigurationWorkflowEvents').mockResolvedValue({ items: [], next_cursor: null })
}

it('创建配置命名空间并发送严格 DTO', async () => {
  vi.spyOn(api, 'listConfigurationNamespaces').mockResolvedValue({ items: [namespace] })
  const create = vi.spyOn(api, 'createConfigurationNamespace').mockResolvedValue(namespace)
  render(<MemoryRouter><ConfigurationsPage principal={principal} /></MemoryRouter>)
  fireEvent.click(await screen.findByRole('button', { name: '新建命名空间' }))
  fireEvent.change(screen.getByLabelText('稳定标识'), { target: { value: 'site_footer' } })
  fireEvent.change(screen.getByLabelText('显示名称'), { target: { value: '页脚配置' } })
  fireEvent.click(screen.getByRole('button', { name: 'OK' }))
  await waitFor(() => expect(create).toHaveBeenCalledWith({ namespace_key: 'site_footer', display_name: '页脚配置', description: '' }))
})

it('保存配置值后用新 Revision 提交审核', async () => {
  mockConfigurationHistory()
  vi.spyOn(api, 'getConfigurationItemValue').mockResolvedValue(itemValue)
  const updated = { ...itemValue, current_draft_revision: { ...itemValue.current_draft_revision, id: 'crv_2', revision_number: 2, value: '400-1234' } }
  const save = vi.spyOn(api, 'updateConfigurationDraft').mockResolvedValue(updated)
  const submit = vi.spyOn(api, 'submitConfigurationItem').mockResolvedValue({ ...updated, current_draft_revision: { ...updated.current_draft_revision, workflow_status: 'pending_review' } })
  render(<MemoryRouter initialEntries={[`/configurations/${namespace.id}/items/cit_1`]}><Routes><Route path="/configurations/:namespaceId/items/:itemId" element={<ConfigurationItemPage principal={principal} />} /></Routes></MemoryRouter>)
  const input = await screen.findByLabelText('配置值')
  await userEvent.clear(input)
  await userEvent.type(input, '400-1234')
  await userEvent.click(screen.getByRole('button', { name: '保存草稿' }))
  await waitFor(() => expect(save).toHaveBeenCalledWith(namespace.id, 'cit_1', 'crv_1', '400-1234'))
  await userEvent.click(screen.getByRole('button', { name: '提交审核' }))
  await waitFor(() => expect(submit).toHaveBeenCalledWith(namespace.id, 'cit_1', 'crv_2'))
})

it('展示 Revision、驳回原因并请求历史下一页', async () => {
  vi.spyOn(api, 'getConfigurationItemValue').mockResolvedValue(itemValue)
  const listRevisions = vi.spyOn(api, 'listConfigurationRevisions')
    .mockResolvedValueOnce({ items: [itemValue.current_draft_revision], next_cursor: 'revisions_2' })
    .mockResolvedValueOnce({ items: [], next_cursor: null })
  vi.spyOn(api, 'listConfigurationWorkflowEvents').mockResolvedValue({ items: [{ id: 'cwe_1', item_id: 'cit_1', namespace_id: namespace.id, revision_id: 'crv_1', type: 'rejected', from_status: 'pending_review', to_status: 'rejected', actor_id: 'usr_2', reason: '请补充客服电话区号', occurred_at: now }], next_cursor: null })
  render(<MemoryRouter initialEntries={[`/configurations/${namespace.id}/items/cit_1`]}><Routes><Route path="/configurations/:namespaceId/items/:itemId" element={<ConfigurationItemPage principal={principal} />} /></Routes></MemoryRouter>)
  expect(await screen.findByText('Revision 1')).toBeInTheDocument()
  expect(await screen.findByText('驳回原因：请补充客服电话区号')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: '下一页 Revision' }))
  await waitFor(() => expect(listRevisions).toHaveBeenLastCalledWith(namespace.id, 'cit_1', 'revisions_2'))
})
