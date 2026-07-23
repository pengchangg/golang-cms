import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import * as client from '../api/client'
import type { Principal } from '../api/types'
import AssetsPage from './AssetsPage'

const principal: Principal = { user_id: 'usr_1', display_name: '测试用户', email: null, auth_method: 'local', is_emergency_admin: false, has_high_risk_role: false, system_permissions: ['assets.view', 'assets.upload', 'assets.archive'], model_permissions: [{ model_id: 'mdl_1', permissions: ['content.view', 'content.create'] }], config_namespace_permissions: [] }

afterEach(() => { cleanup(); vi.restoreAllMocks() })

describe('F3 页面', () => {
  it('素材列表展示状态并提供上传入口', async () => {
    vi.spyOn(client.api, 'listAssets').mockResolvedValue({ items: [{ id: 'ast_1', filename: '封面.png', mime_type: 'image/png', preview_kind: 'image', size: 1024, sha256: 'a'.repeat(64), etag: 'etag', status: 'available', created_by: 'usr_1', created_at: '2026-07-19T08:00:00Z', confirmed_at: '2026-07-19T08:00:00Z', archived_at: null }], next_cursor: null })
    render(<MemoryRouter><AssetsPage principal={principal} /></MemoryRouter>)
    expect(await screen.findByText('封面.png')).toBeVisible()
    expect(screen.getByText('可用')).toBeVisible()
    expect(screen.getByRole('button', { name: '上传素材' })).toBeEnabled()
  })

  it('可以重新确认待确认素材', async () => {
    const asset = { id: 'ast_pending', filename: '待确认.png', mime_type: 'image/png', preview_kind: 'image' as const, size: 1024, sha256: 'b'.repeat(64), etag: null, status: 'quarantined' as const, created_by: 'usr_1', created_at: '2026-07-19T08:00:00Z', confirmed_at: null, archived_at: null }
    vi.spyOn(client.api, 'listAssets').mockResolvedValue({ items: [asset], next_cursor: null })
    const confirm = vi.spyOn(client.api, 'confirmAssetUpload').mockResolvedValue({ ...asset, status: 'available', etag: 'etag', confirmed_at: '2026-07-19T08:05:00Z' })
    render(<MemoryRouter><AssetsPage principal={principal} /></MemoryRouter>)

    fireEvent.click(await screen.findByRole('button', { name: '确认可用' }))
    fireEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: '确认可用' }))

    await waitFor(() => expect(confirm).toHaveBeenCalledWith('ast_pending'))
  })

  it('可以废弃待确认素材', async () => {
    const asset = { id: 'ast_pending', filename: '待确认.png', mime_type: 'image/png', preview_kind: 'image' as const, size: 1024, sha256: 'b'.repeat(64), etag: null, status: 'quarantined' as const, created_by: 'usr_1', created_at: '2026-07-19T08:00:00Z', confirmed_at: null, archived_at: null }
    vi.spyOn(client.api, 'listAssets').mockResolvedValue({ items: [asset], next_cursor: null })
    const discard = vi.spyOn(client.api, 'discardQuarantinedAsset').mockResolvedValue(undefined)
    render(<MemoryRouter><AssetsPage principal={principal} /></MemoryRouter>)

    fireEvent.click(await screen.findByRole('button', { name: '废弃' }))
    fireEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: /废\s*弃/ }))

    await waitFor(() => expect(discard).toHaveBeenCalledWith('ast_pending'))
  })
})
