import { Button, Modal, Space, Upload, Typography, message } from 'antd'
import type { UploadFile } from 'antd'
import { useState } from 'react'
import { useNavigate } from 'react-router-dom'

import { ASSET_MAX_BYTES, ASSET_MAX_LABEL, adminDownloadUrl, api, hashUploadFile, putSignedUpload } from '../api/client'
import type { CreateExportRequest, Principal } from '../api/types'
import { hasModelPermission, hasSystemPermission } from '../auth/permissions'
import { ASSETS_ENABLED } from '../config'

export function TransferActions({ principal, modelId, exportQuery }: { principal: Principal; modelId: string; exportQuery: CreateExportRequest }) {
  const navigate = useNavigate()
  const [open, setOpen] = useState(false)
  const [files, setFiles] = useState<UploadFile[]>([])
  const [busy, setBusy] = useState(false)
  const canExecute = hasSystemPermission(principal, 'transfers.execute')
  const canDownload = hasSystemPermission(principal, 'transfers.download')
  const canImport = canExecute && hasModelPermission(principal, modelId, 'content.create')
  const canExport = canExecute && hasModelPermission(principal, modelId, 'content.view')

  if (!ASSETS_ENABLED) return null

  async function importCSV() {
    const file = files[0]?.originFileObj
    if (!file) return
    setBusy(true)
    try {
      const sha256 = await hashUploadFile(file)
      const upload = await api.createImportUpload(modelId, { filename: file.name, size: file.size, sha256 })
      await putSignedUpload(upload, file)
      const job = await api.createImport(modelId, upload.upload_id)
      message.success('导入任务已创建')
      navigate(`/jobs/${job.id}`)
    } finally { setBusy(false) }
  }

  async function exportCSV() {
    setBusy(true)
    try {
      const job = await api.createExport(modelId, exportQuery)
      message.success('导出任务已创建')
      navigate(`/jobs/${job.id}`)
    } finally { setBusy(false) }
  }

  return <><Space wrap className="transfer-actions"><Button href={adminDownloadUrl(`/models/${encodeURIComponent(modelId)}/transfers/template`)} disabled={!canDownload || !hasModelPermission(principal, modelId, 'content.view')}>下载 CSV 模板</Button><Button disabled={!canImport} onClick={() => setOpen(true)}>导入 CSV</Button><Button disabled={!canExport} loading={busy} onClick={exportCSV}>按当前筛选导出</Button></Space><Modal title="导入 CSV" open={open} okText="上传并创建任务" onCancel={() => !busy && setOpen(false)} onOk={importCSV} okButtonProps={{ loading: busy, disabled: files.length !== 1 }}><Typography.Paragraph type="secondary">仅接受 UTF-8 CSV。表头必须与当前模型全部 active 根字段 key 及顺序完全一致。浏览器哈希不支持流式计算，文件硬上限为 {ASSET_MAX_LABEL}。</Typography.Paragraph><Upload.Dragger accept=".csv,text/csv" maxCount={1} fileList={files} beforeUpload={(file) => { if (file.size <= ASSET_MAX_BYTES) return false; message.error(`文件“${file.name}”超过 ${ASSET_MAX_LABEL} 上限，已拒绝读取`); return Upload.LIST_IGNORE }} onChange={({ fileList }) => setFiles(fileList.slice(-1))}><p>点击或拖入一个 CSV 文件</p><p className="ant-upload-hint">最大 {ASSET_MAX_LABEL}</p></Upload.Dragger></Modal></>
}
