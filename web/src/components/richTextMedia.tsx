import { DeleteOutlined, DownloadOutlined, SwapOutlined } from '@ant-design/icons'
import type { Editor } from '@tiptap/core'
import type { NodeViewProps } from '@tiptap/react'
import { NodeViewWrapper } from '@tiptap/react'
import { Button, Empty, Input, Space, Tag, Typography } from 'antd'
import { useContext } from 'react'

import type { ReferencedAsset } from '../api/types'
import { RichTextMediaContext } from './richTextMediaContext'

export type RichTextMediaKind = 'image' | 'audio' | 'video'

export interface RichTextMediaEnvironment {
  assets: Record<string, ReferencedAsset>
  disabled: boolean
  canSelect: boolean
  previewUrl: (assetID: string) => string | undefined
  downloadUrl: (assetID: string) => string | undefined
  replace: (kind: RichTextMediaKind, editor: Editor, getPos: NodeViewProps['getPos']) => void
}

export function RichTextMediaView({ node, editor, getPos, updateAttributes, deleteNode, selected }: NodeViewProps) {
  const environment = useContext(RichTextMediaContext)
  const kind = node.type.name as RichTextMediaKind
  const assetID = String(node.attrs.asset_id ?? '')
  const asset = environment?.assets[assetID]
  const previewUrl = environment?.previewUrl(assetID)
  const downloadUrl = environment?.downloadUrl(assetID)
  const disabled = environment?.disabled ?? true
  const title = asset?.filename ?? assetID

  return <NodeViewWrapper className={`rich-text-media is-${kind}${selected ? ' is-selected' : ''}`} data-drag-handle="">
    <div className="rich-text-media-content" contentEditable={false}>
      {kind === 'image' && previewUrl ? <img src={previewUrl} alt={String(node.attrs.alt ?? '')} /> : null}
      {kind === 'audio' && previewUrl ? <audio src={previewUrl} controls preload="metadata">浏览器不支持音频播放。</audio> : null}
      {kind === 'video' && previewUrl ? <video src={previewUrl} controls preload="metadata">浏览器不支持视频播放。</video> : null}
      {!previewUrl ? <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="素材预览不可用" /> : null}
    </div>
    <div className="rich-text-media-meta" contentEditable={false}>
      <div><Typography.Text strong>{title}</Typography.Text><Typography.Text type="secondary"><code>{assetID}</code></Typography.Text></div>
      <Space wrap>
        <Tag>{kind === 'image' ? '图片' : kind === 'audio' ? '音频' : '视频'}</Tag>
        {asset?.status === 'archived' ? <Tag>已归档</Tag> : null}
        {!disabled && environment?.canSelect ? <Button size="small" icon={<SwapOutlined />} onClick={() => environment.replace(kind, editor, getPos)}>替换</Button> : null}
        {downloadUrl ? <Button size="small" icon={<DownloadOutlined />} href={downloadUrl}>下载</Button> : null}
        {!disabled ? <Button size="small" danger icon={<DeleteOutlined />} onClick={deleteNode}>删除</Button> : null}
      </Space>
    </div>
    {kind === 'image' ? <label className="rich-text-media-alt">替代文本<Input value={String(node.attrs.alt ?? '')} maxLength={1000} disabled={disabled} onChange={(event) => updateAttributes({ alt: event.target.value })} /></label> : null}
  </NodeViewWrapper>
}
