import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '../api/client'
import type { Asset } from '../api/types'
import { AssetPicker } from './AssetPicker'

const uploaded: Asset = { id: 'ast_new', filename: '新封面.png', mime_type: 'image/png', preview_kind: 'image', size: 1024, sha256: 'a'.repeat(64), etag: 'etag', status: 'available', created_by: 'usr_1', created_at: '2026-07-20T08:00:00Z', confirmed_at: '2026-07-20T08:00:00Z', archived_at: null }

vi.mock('./AssetUploadModal', () => ({
  AssetUploadModal: ({ open, onUploaded }: { open: boolean; onUploaded: (asset: Asset) => void }) => open ? <button onClick={() => onUploaded(uploaded)}>模拟确认上传</button> : null,
}))

afterEach(() => { cleanup(); vi.restoreAllMocks() })

describe('素材选择器上传', () => {
  it('单素材确认上传后自动选中并刷新列表', async () => {
    vi.spyOn(api, 'listAssets').mockResolvedValue({ items: [], next_cursor: null })
    const onChange = vi.fn()
    render(<AssetPicker multiple={false} value={null} onChange={onChange} canUpload />)
    await userEvent.click(screen.getByRole('button', { name: '选择素材' }))
    await userEvent.click(screen.getByRole('button', { name: '上传并选中' }))
    await userEvent.click(screen.getByRole('button', { name: '模拟确认上传' }))
    expect(onChange).toHaveBeenCalledWith('ast_new')
    expect(api.listAssets).toHaveBeenCalledWith(expect.objectContaining({ status: 'available' }))
  })

  it('没有上传权限时仍可选择已有素材但禁用上传', async () => {
    vi.spyOn(api, 'listAssets').mockResolvedValue({ items: [uploaded], next_cursor: null })
    render(<AssetPicker multiple={false} value={null} onChange={vi.fn()} />)
    await userEvent.click(screen.getByRole('button', { name: '选择素材' }))
    expect(await screen.findByRole('button', { name: '上传并选中' })).toBeDisabled()
    expect(screen.getByRole('button', { name: '新封面.png' })).toBeEnabled()
  })

  it('按媒体类型在服务端分页筛选素材', async () => {
    vi.spyOn(api, 'listAssets').mockResolvedValue({ items: [uploaded], next_cursor: null })
    render(<AssetPicker multiple={false} value={null} onChange={vi.fn()} kind="image" triggerLabel="插入图片" />)
    await userEvent.click(screen.getByRole('button', { name: '插入图片' }))
    expect(api.listAssets).toHaveBeenCalledWith({ status: 'available', kind: 'image', cursor: undefined, limit: 20 })
    await waitFor(() => expect(screen.getByRole('dialog', { name: '选择可用图片' })).toBeVisible())
  })

  it('表单变为只读时关闭已打开的选择和上传弹窗', async () => {
    vi.spyOn(api, 'listAssets').mockResolvedValue({ items: [], next_cursor: null })
    const view = render(<AssetPicker multiple={false} value={null} onChange={vi.fn()} canUpload />)
    await userEvent.click(screen.getByRole('button', { name: '选择素材' }))
    await userEvent.click(screen.getByRole('button', { name: '上传并选中' }))
    expect(screen.getByRole('button', { name: '模拟确认上传' })).toBeVisible()
    view.rerender(<AssetPicker multiple={false} value={null} onChange={vi.fn()} canUpload disabled />)
    expect(screen.queryByRole('button', { name: '模拟确认上传' })).not.toBeInTheDocument()
    expect(screen.queryByRole('dialog', { name: '选择可用素材' })).not.toBeInTheDocument()
  })
})
