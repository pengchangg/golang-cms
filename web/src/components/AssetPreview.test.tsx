import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type { ReferencedAsset } from '../api/types'
import { AssetPreview } from './AssetPreview'

const base: ReferencedAsset = { id: 'ast_1', filename: '封面.svg', mime_type: 'image/svg+xml', size: 100, status: 'available', preview_kind: 'image' }

afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals() })

describe('AssetPreview', () => {
  it('显示 48px 图片缩略图并提供放大入口', async () => {
    render(<AssetPreview asset={base} previewUrl="/preview" downloadUrl="/download" />)
    const image = screen.getByRole('img', { name: '封面.svg' })
    expect(image).toHaveAttribute('src', '/preview')
    expect(image.closest('.ant-image')).toHaveStyle({ width: '48px', height: '48px' })
    await userEvent.click(image)
    expect(await screen.findByRole('dialog')).toBeVisible()
  })

  it('图片加载失败后降级为图标和下载', () => {
    render(<AssetPreview asset={base} previewUrl="/preview" downloadUrl="/download" />)
    fireEvent.error(screen.getByRole('img', { name: '封面.svg' }))
    expect(screen.getByLabelText('图片文件')).toBeVisible()
    expect(screen.getByRole('link', { name: '下载 封面.svg 文件名' })).toHaveAttribute('href', '/download')
  })

  it('紧凑图片使用 34px 尺寸', () => {
    render(<AssetPreview asset={base} previewUrl="/preview" downloadUrl="/download" compact />)
    expect(screen.getByRole('img', { name: '封面.svg' }).closest('.ant-image')).toHaveStyle({ width: '34px', height: '34px' })
  })

  it('none 文件名直接下载并展示归档标记', () => {
    render(<AssetPreview asset={{ ...base, filename: '资料.zip', mime_type: 'application/zip', preview_kind: 'none', status: 'archived' }} previewUrl="/preview" downloadUrl="/download" />)
    expect(screen.getByLabelText('压缩包文件')).toBeVisible()
    expect(screen.getByRole('link', { name: '下载 资料.zip 文件名' })).toHaveAttribute('href', '/download')
    expect(screen.getByText('已归档')).toBeVisible()
  })

  it('打开 PDF 预览', async () => {
    render(<AssetPreview asset={{ ...base, filename: '手册.pdf', mime_type: 'application/pdf', preview_kind: 'pdf' }} previewUrl="/manual-preview" downloadUrl="/download" />)
    await userEvent.click(screen.getByRole('button', { name: '预览 手册.pdf 文件名' }))
    expect(screen.getByTitle('手册.pdf PDF 预览')).toHaveAttribute('src', '/manual-preview')
    expect(screen.getByRole('link', { name: 'download 下载' })).toHaveAttribute('href', '/download')
  })

  it('同源获取并格式化 JSON 文本', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('{"ok":true}'))
    vi.stubGlobal('fetch', fetchMock)
    render(<AssetPreview asset={{ ...base, filename: 'data.json', mime_type: 'application/json', preview_kind: 'text' }} previewUrl="/text-preview" downloadUrl="/download" />)
    await userEvent.click(screen.getByRole('button', { name: '预览 data.json 文件名' }))
    expect(await screen.findByText(/"ok": true/)).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledWith('/text-preview', expect.objectContaining({ credentials: 'same-origin' }))
  })

  it('视频加载失败后显示下载降级提示', async () => {
    render(<AssetPreview asset={{ ...base, filename: '演示.mp4', mime_type: 'video/mp4', preview_kind: 'video' }} previewUrl="/video-preview" downloadUrl="/download" />)
    await userEvent.click(screen.getByRole('button', { name: '预览 演示.mp4 文件名' }))
    fireEvent.error(document.querySelector('video')!)
    expect(await screen.findByRole('alert')).toHaveTextContent('浏览器无法预览此媒体')
    expect(screen.getByRole('link', { name: 'download 下载' })).toHaveAttribute('href', '/download')
  })

  it('隔离素材不可预览或下载', async () => {
    render(<AssetPreview asset={{ ...base, status: 'quarantined' }} previewUrl="/preview" downloadUrl="/download" />)
    expect(screen.queryByRole('img', { name: '封面.svg' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: '封面.svg 不可预览或下载' })).toBeDisabled()
    await waitFor(() => expect(screen.queryByRole('link')).not.toBeInTheDocument())
  })
})
