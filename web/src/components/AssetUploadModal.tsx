import { Modal, Typography, Upload, message } from 'antd'
import type { UploadFile } from 'antd'
import { useState } from 'react'

import { ASSET_MAX_BYTES, ASSET_MAX_LABEL, apiErrorMessage, uploadAssetFile } from '../api/client'
import type { Asset, AssetKind } from '../api/types'

const kindMimeTypes: Record<AssetKind, string[]> = {
  image: ['image/jpeg', 'image/png', 'image/gif', 'image/webp', 'image/avif'],
  audio: ['audio/mpeg', 'audio/mp4', 'audio/ogg', 'audio/wav', 'audio/webm'],
  video: ['video/mp4', 'video/webm'],
}

export function AssetUploadModal({ open, onCancel, onUploaded, disabled = false, kind }: { open: boolean; onCancel: () => void; onUploaded: (asset: Asset) => void; disabled?: boolean; kind?: AssetKind }) {
  const [files, setFiles] = useState<UploadFile[]>([])
  const [uploading, setUploading] = useState(false)

  async function upload() {
    if (disabled) return
    const file = files[0]?.originFileObj
    if (!file) return
    setUploading(true)
    try {
      const asset = await uploadAssetFile(file)
      setFiles([])
      onUploaded(asset)
    } catch (error) {
      message.error(apiErrorMessage(error, '素材上传失败'))
    } finally {
      setUploading(false)
    }
  }

  function close() {
    if (uploading) return
    setFiles([])
    onCancel()
  }

  return <Modal title="上传素材" open={open} okText="上传并确认" onCancel={close} onOk={upload} okButtonProps={{ loading: uploading, disabled: disabled || files.length !== 1 }}>
    <Typography.Paragraph type="secondary">选择文件后将在本机计算 SHA-256，再申请一次性 PUT 地址并确认对象元数据。浏览器哈希不支持流式计算，文件硬上限为 {ASSET_MAX_LABEL}。</Typography.Paragraph>
    <Upload.Dragger accept={kind ? kindMimeTypes[kind].join(',') : undefined} disabled={disabled} maxCount={1} fileList={files} beforeUpload={(file) => { if (kind && !kindMimeTypes[kind].includes(file.type)) { message.error(`请选择支持的${kind === 'image' ? '图片' : kind === 'audio' ? '音频' : '视频'}格式`); return Upload.LIST_IGNORE } if (file.size <= ASSET_MAX_BYTES) return false; message.error(`文件“${file.name}”超过 ${ASSET_MAX_LABEL} 上限，已拒绝读取`); return Upload.LIST_IGNORE }} onChange={({ fileList }) => setFiles(fileList.slice(-1))}><p>点击或拖入一个文件</p><p className="ant-upload-hint">最大 {ASSET_MAX_LABEL}，类型仍由服务端配置校验</p></Upload.Dragger>
  </Modal>
}
