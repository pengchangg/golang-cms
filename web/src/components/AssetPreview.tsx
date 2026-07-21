import { DownloadOutlined, EyeOutlined, InboxOutlined } from '@ant-design/icons'
import { Alert, Button, Image, Modal, Spin, Tag, Tooltip } from 'antd'
import { useEffect, useState } from 'react'

import type { ReferencedAsset } from '../api/types'
import { AssetMimeIcon } from './assetMimeIcon'

type DisplayAsset = Pick<ReferencedAsset, 'id' | 'filename' | 'mime_type' | 'size' | 'status' | 'preview_kind'>

function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : '预览加载失败'
}

export function AssetPreview({ asset, previewUrl, downloadUrl, compact = false }: { asset: DisplayAsset; previewUrl: string; downloadUrl: string; compact?: boolean }) {
  const [open, setOpen] = useState(false)
  const [imageFailed, setImageFailed] = useState(false)
  const [text, setText] = useState<string>()
  const [textError, setTextError] = useState<string>()
  const [mediaError, setMediaError] = useState(false)
  const quarantined = asset.status === 'quarantined'
  const canPreview = asset.preview_kind !== 'none' && !quarantined && !(asset.preview_kind === 'image' && imageFailed)
  const canDownload = !quarantined

  useEffect(() => {
    if (!open || asset.preview_kind !== 'text') return
    const controller = new AbortController()
    void fetch(previewUrl, { credentials: 'same-origin', signal: controller.signal }).then(async (response) => {
      if (response.ok) return response.text()
      let message = `预览加载失败（${response.status}）`
      try {
        const payload = await response.json() as { error?: { message?: string } }
        if (payload.error?.message) message = payload.error.message
      } catch { /* 使用状态码错误。 */ }
      throw new Error(message)
    }).then((value) => setText(value)).catch((error: unknown) => {
      if (!controller.signal.aborted) setTextError(errorMessage(error))
    })
    return () => controller.abort()
  }, [asset.preview_kind, open, previewUrl])

  let formattedText = text
  if (text !== undefined && (asset.mime_type === 'application/json' || asset.filename.toLowerCase().endsWith('.json'))) {
    try { formattedText = JSON.stringify(JSON.parse(text), null, 2) } catch { /* 非法 JSON 按原文展示。 */ }
  }

  const close = () => setOpen(false)
  const showPreview = () => {
    setMediaError(false)
    if (asset.preview_kind === 'text') {
      setText(undefined)
      setTextError(undefined)
    }
    setOpen(true)
  }
  const name = asset.filename
  const previewLabel = `预览 ${name}`
  const downloadLabel = `下载 ${name}`
  const icon = <span className="asset-preview-icon"><AssetMimeIcon mimeType={asset.mime_type} filename={name} /></span>
  const nameAction = canPreview ? <button type="button" className="asset-preview-name" title={previewLabel} aria-label={`${previewLabel} 文件名` } onClick={showPreview}>{name}</button>
    : canDownload ? <a className="asset-preview-name" href={downloadUrl} title={downloadLabel} aria-label={`${downloadLabel} 文件名`}>{name}</a>
      : <span className="asset-preview-name is-disabled" title="隔离中的素材不可操作">{name}</span>

  return <div className={`asset-preview${compact ? ' is-compact' : ''}`}>
    {asset.preview_kind === 'image' && canPreview ? <Image className="asset-preview-thumbnail" width={compact ? 34 : 48} height={compact ? 34 : 48} src={previewUrl} alt={name} preview={{ open, onOpenChange: setOpen }} onError={() => setImageFailed(true)} /> : icon}
    <div className="asset-preview-meta">
      {nameAction}
      <span className="asset-preview-detail">{asset.mime_type || '未知类型'}{asset.status === 'archived' ? <Tag variant="filled">已归档</Tag> : null}</span>
    </div>
    <div className="asset-preview-actions">
      {canPreview && asset.preview_kind !== 'image' ? <Tooltip title={previewLabel}><Button type="text" icon={<EyeOutlined />} aria-label={previewLabel} onClick={showPreview} /></Tooltip> : null}
      {canDownload ? <Tooltip title={downloadLabel}><Button type="text" icon={<DownloadOutlined />} href={downloadUrl} aria-label={downloadLabel} /></Tooltip> : <Tooltip title="隔离中的素材不可预览或下载"><Button type="text" disabled icon={<InboxOutlined />} aria-label={`${name} 不可预览或下载`} /></Tooltip>}
    </div>
    {asset.preview_kind !== 'image' ? <Modal className="asset-preview-modal" width={asset.preview_kind === 'pdf' ? 920 : 760} title={name} open={open} footer={canDownload ? <Button icon={<DownloadOutlined />} href={downloadUrl}>下载</Button> : null} onCancel={close} destroyOnHidden>
      <div className="asset-preview-stage">
        {open && asset.preview_kind === 'pdf' ? <><iframe src={previewUrl} title={`${name} PDF 预览`} /><p className="asset-preview-fallback">如果浏览器无法显示 PDF，请使用下方下载按钮查看。</p></> : null}
        {open && asset.preview_kind === 'video' && !mediaError ? <video src={previewUrl} controls preload="metadata" onError={() => setMediaError(true)}>浏览器不支持视频预览。</video> : null}
        {open && asset.preview_kind === 'audio' && !mediaError ? <audio src={previewUrl} controls preload="metadata" onError={() => setMediaError(true)}>浏览器不支持音频预览。</audio> : null}
        {open && mediaError ? <Alert type="warning" showIcon title="浏览器无法预览此媒体" description="可以下载文件后使用本地播放器打开。" /> : null}
        {open && asset.preview_kind === 'text' && text === undefined && !textError ? <Spin description="正在加载文本"><span className="asset-preview-loading" /></Spin> : null}
        {open && asset.preview_kind === 'text' && textError ? <Alert type="error" showIcon message="无法预览文本" description={textError} /> : null}
        {open && asset.preview_kind === 'text' && formattedText !== undefined ? <pre>{formattedText}</pre> : null}
      </div>
    </Modal> : null}
  </div>
}
