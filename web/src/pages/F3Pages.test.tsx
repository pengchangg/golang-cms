import { cleanup, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import * as client from '../api/client'
import type { Principal } from '../api/types'
import AssetsPage from './AssetsPage'

const principal: Principal = { user_id: 'usr_1', display_name: '测试用户', email: null, auth_method: 'local', system_permissions: ['assets.view', 'assets.upload', 'assets.archive'], model_permissions: [{ model_id: 'mdl_1', permissions: ['content.view', 'content.create'] }] }

afterEach(() => { cleanup(); vi.restoreAllMocks() })

describe('F3 页面', () => {
  it('素材列表展示状态并提供上传入口', async () => {
    vi.spyOn(client.api, 'listAssets').mockResolvedValue({ items: [{ id: 'ast_1', filename: '封面.png', mime_type: 'image/png', size: 1024, sha256: 'a'.repeat(64), etag: 'etag', status: 'available', created_by: 'usr_1', created_at: '2026-07-19T08:00:00Z', confirmed_at: '2026-07-19T08:00:00Z', archived_at: null }], next_cursor: null })
    render(<MemoryRouter><AssetsPage principal={principal} /></MemoryRouter>)
    expect(await screen.findByText('封面.png')).toBeVisible()
    expect(screen.getByText('可用')).toBeVisible()
    expect(screen.getByRole('button', { name: '上传素材' })).toBeEnabled()
  })
})
