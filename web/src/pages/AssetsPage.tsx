import { Button, Input, Modal, Select, Space, Table, Tag, Typography, Upload, message } from 'antd'
import type { UploadFile } from 'antd'
import { useState } from 'react'

import { ASSET_MAX_BYTES, ASSET_MAX_LABEL, adminDownloadUrl, api, hashUploadFile, putSignedUpload } from '../api/client'
import type { Asset, AssetStatus, Principal } from '../api/types'
import { hasSystemPermission } from '../auth/permissions'
import { DataState, PageHeader, PendingApiNotice, useApiData } from '../components/Page'

const labels: Record<AssetStatus, string> = { quarantined: '待确认', available: '可用', archived: '已归档' }

export default function AssetsPage({ principal }: { principal: Principal }) {
  const [status, setStatus] = useState<AssetStatus | undefined>()
  const [mimeType, setMimeType] = useState('')
  const [cursors, setCursors] = useState<Array<string | undefined>>([undefined])
  const [uploadOpen, setUploadOpen] = useState(false)
  const [files, setFiles] = useState<UploadFile[]>([])
  const [uploading, setUploading] = useState(false)
  const assets = useApiData(() => api.listAssets({ status, mime_type: mimeType || undefined, cursor: cursors.at(-1), limit: 20 }), [status, mimeType, cursors.at(-1)])
  const canUpload = hasSystemPermission(principal, 'assets.upload')
  const canArchive = hasSystemPermission(principal, 'assets.archive')

  async function upload() {
    const file = files[0]?.originFileObj
    if (!file) return
    setUploading(true)
    try {
      const sha256 = await hashUploadFile(file)
      const signed = await api.createAssetUpload({ filename: file.name, mime_type: file.type, size: file.size, sha256 })
      await putSignedUpload(signed.upload, file)
      await api.confirmAssetUpload(signed.asset.id)
      message.success('素材已上传并确认可用')
      setUploadOpen(false)
      setFiles([])
      setCursors([undefined])
      assets.reload()
    } catch (error) {
      message.error(error instanceof Error ? error.message : '素材上传失败')
    } finally {
      setUploading(false)
    }
  }

  async function archive(asset: Asset) {
    await api.archiveAsset(asset.id)
    message.success('素材已归档，历史发布引用仍可下载')
    assets.reload()
  }

  return <><PageHeader eyebrow="素材工作区" title="素材库" description="浏览器直传私有对象存储，确认完整性后才允许进入内容版本。" extra={<Button type="primary" disabled={!canUpload} onClick={() => setUploadOpen(true)}>上传素材</Button>} /><PendingApiNotice />
    <Space wrap className="filter-bar"><Select aria-label="素材状态" allowClear placeholder="全部状态" value={status} options={Object.entries(labels).map(([value, label]) => ({ value, label }))} onChange={(value) => { setStatus(value); setCursors([undefined]) }} /><Input aria-label="MIME 类型" placeholder="精确 MIME，如 image/png" value={mimeType} onChange={(event) => { setMimeType(event.target.value); setCursors([undefined]) }} /></Space>
    <DataState loading={assets.loading} error={assets.error} empty={!assets.data?.items.length} retry={assets.reload}><Table<Asset> rowKey="id" dataSource={assets.data?.items} pagination={false} scroll={{ x: 820 }} columns={[
      { title: '文件名', dataIndex: 'filename' },
      { title: '状态', dataIndex: 'status', render: (value: AssetStatus) => <Tag color={value === 'available' ? 'green' : value === 'quarantined' ? 'gold' : 'default'}>{labels[value]}</Tag> },
      { title: '类型', dataIndex: 'mime_type' },
      { title: '大小', dataIndex: 'size', render: (value: number) => `${(value / 1024).toFixed(value < 10240 ? 1 : 0)} KiB` },
      { title: '素材 ID', dataIndex: 'id', render: (value: string) => <Typography.Text copyable><code>{value}</code></Typography.Text> },
      { title: '操作', render: (_, asset) => <Space><Button type="link" href={adminDownloadUrl(`/assets/${encodeURIComponent(asset.id)}/download`)} disabled={asset.status === 'quarantined'}>签名下载</Button>{asset.status === 'available' ? <Button type="link" danger disabled={!canArchive} onClick={() => Modal.confirm({ title: `归档 ${asset.filename}？`, content: '不可恢复，但不会破坏已有线上引用。', okText: '归档', okButtonProps: { danger: true }, onOk: () => archive(asset) })}>归档</Button> : null}</Space> },
    ]} /></DataState>
    <Space className="pagination-actions"><Button disabled={cursors.length === 1} onClick={() => setCursors((values) => values.slice(0, -1))}>上一页</Button><Button disabled={!assets.data?.next_cursor} onClick={() => assets.data?.next_cursor && setCursors((values) => [...values, assets.data!.next_cursor!])}>下一页</Button></Space>
    <Modal title="上传素材" open={uploadOpen} okText="上传并确认" onCancel={() => !uploading && setUploadOpen(false)} onOk={upload} okButtonProps={{ loading: uploading, disabled: files.length !== 1 }}>
      <Typography.Paragraph type="secondary">选择文件后将在本机计算 SHA-256，再申请一次性 PUT 地址并确认对象元数据。浏览器哈希不支持流式计算，文件硬上限为 {ASSET_MAX_LABEL}。</Typography.Paragraph>
      <Upload.Dragger maxCount={1} fileList={files} beforeUpload={(file) => { if (file.size <= ASSET_MAX_BYTES) return false; message.error(`文件“${file.name}”超过 ${ASSET_MAX_LABEL} 上限，已拒绝读取`); return Upload.LIST_IGNORE }} onChange={({ fileList }) => setFiles(fileList.slice(-1))}><p>点击或拖入一个文件</p><p className="ant-upload-hint">最大 {ASSET_MAX_LABEL}，类型仍由服务端配置校验</p></Upload.Dragger>
    </Modal>
  </>
}
