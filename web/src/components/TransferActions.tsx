import { Button, Modal, Space, Upload, Typography, message } from 'antd'
import type { UploadFile } from 'antd'
import { useState } from 'react'

import { adminDownloadUrl, api } from '../api/client'
import type { ExportCSVQuery, Principal } from '../api/types'
import { hasModelPermission } from '../auth/permissions'

const CSV_MAX_BYTES = 10 * 1024 * 1024
const CSV_MAX_LABEL = '10 MiB'

export function TransferActions({ principal, modelId, exportQuery, onImported }: { principal: Principal; modelId: string; exportQuery: ExportCSVQuery; onImported: () => void }) {
  const [open, setOpen] = useState(false)
  const [files, setFiles] = useState<UploadFile[]>([])
  const [importing, setImporting] = useState(false)
  const [exporting, setExporting] = useState(false)
  const canImport = hasModelPermission(principal, modelId, 'content.create')
  const canView = hasModelPermission(principal, modelId, 'content.view')

  async function submitImport() {
    const file = files[0]?.originFileObj
    if (!file) return
    setImporting(true)
    try {
      const result = await api.importCSV(modelId, file)
      message.success(`成功导入 ${result.imported} 条内容`)
      setOpen(false)
      setFiles([])
      onImported()
    } catch (error) {
      message.error(error instanceof Error ? error.message : 'CSV 导入失败')
    } finally { setImporting(false) }
  }

  async function downloadExport() {
    setExporting(true)
    try {
      const blob = await api.exportCSV(modelId, exportQuery)
      const url = URL.createObjectURL(blob)
      try {
        const link = document.createElement('a')
        link.href = url
        link.download = `${modelId}.csv`
        link.click()
      } finally {
        URL.revokeObjectURL(url)
      }
    } catch (error) {
      message.error(error instanceof Error ? error.message : 'CSV 导出失败')
    } finally { setExporting(false) }
  }

  return <><Space wrap className="transfer-actions"><Button href={adminDownloadUrl(`/models/${encodeURIComponent(modelId)}/transfers/template`)} disabled={!canView}>下载 CSV 模板</Button><Button disabled={!canImport} onClick={() => setOpen(true)}>导入 CSV</Button><Button disabled={!canView} loading={exporting} onClick={downloadExport}>按当前筛选导出</Button></Space><Modal title="导入 CSV" open={open} okText="直接导入" onCancel={() => !importing && setOpen(false)} onOk={submitImport} okButtonProps={{ loading: importing, disabled: files.length !== 1 }}><Typography.Paragraph type="secondary">仅接受 UTF-8 CSV，最多 1000 行、{CSV_MAX_LABEL}。表头必须与当前模型全部 active 根字段 key 及顺序完全一致。</Typography.Paragraph><Upload.Dragger accept=".csv,text/csv" maxCount={1} fileList={files} beforeUpload={(file) => { if (file.size <= CSV_MAX_BYTES) return false; message.error(`文件“${file.name}”超过 ${CSV_MAX_LABEL} 上限`); return Upload.LIST_IGNORE }} onChange={({ fileList }) => setFiles(fileList.slice(-1))}><p>点击或拖入一个 CSV 文件</p><p className="ant-upload-hint">最多 1000 行，最大 {CSV_MAX_LABEL}</p></Upload.Dragger></Modal></>
}
