import { Button, Modal, Space, Table, Typography } from 'antd'
import { useState } from 'react'

import { adminDownloadUrl, api } from '../api/client'
import type { Asset, ReferencedAsset } from '../api/types'
import { AssetPreview } from './AssetPreview'
import { DataState, useApiData } from './Page'
import { AssetUploadModal } from './AssetUploadModal'

function assetUrls(id: string) {
  const encoded = encodeURIComponent(id)
  return {
    previewUrl: adminDownloadUrl(`/assets/${encoded}/preview`),
    downloadUrl: adminDownloadUrl(`/assets/${encoded}/download`),
  }
}

export function AssetPicker({ multiple, value, onChange, disabled, canUpload = false, knownAssets = {}, labelledBy, describedBy }: { multiple: boolean; value: string | string[] | null; onChange: (value: string | string[] | null) => void; disabled?: boolean; canUpload?: boolean; knownAssets?: Record<string, ReferencedAsset>; labelledBy?: string; describedBy?: string }) {
  const [open, setOpen] = useState(false)
  const [uploadOpen, setUploadOpen] = useState(false)
  const [cursors, setCursors] = useState<Array<string | undefined>>([undefined])
  const [selectedAssets, setSelectedAssets] = useState<Record<string, Asset>>({})
  const assets = useApiData(() => open ? api.listAssets({ status: 'available', cursor: cursors.at(-1), limit: 20 }) : Promise.resolve({ items: [], next_cursor: null }), [open, cursors.at(-1)])
  const selected = multiple ? (Array.isArray(value) ? value : []) : (typeof value === 'string' ? [value] : [])
  const assetByID: Record<string, ReferencedAsset> = { ...knownAssets, ...selectedAssets }
  const actionID = labelledBy ? `${labelledBy}-asset-action` : undefined

  function choose(asset: Asset) {
    if (disabled) return
    setSelectedAssets((current) => ({ ...current, [asset.id]: asset }))
    if (!multiple) {
      onChange(asset.id)
      setOpen(false)
      return
    }
    if (selected.includes(asset.id) || selected.length >= 50) return
    onChange([...selected, asset.id])
  }

  function uploaded(asset: Asset) {
    if (disabled) return
    setUploadOpen(false)
    setCursors([undefined])
    assets.reload()
    choose(asset)
  }

  return <div className="asset-picker"><Space wrap>
    <Button aria-labelledby={labelledBy && actionID ? `${labelledBy} ${actionID}` : undefined} aria-describedby={describedBy} onClick={() => setOpen(true)} disabled={disabled}><span id={actionID}>{multiple ? '选择素材' : selected.length ? '更换素材' : '选择素材'}</span></Button>
    {selected.length ? <Typography.Text type="secondary">已选 {selected.length}{multiple ? ' / 50' : ''}</Typography.Text> : <Typography.Text type="secondary">未选择</Typography.Text>}
  </Space>
  <div className="asset-selection" aria-label="已选素材" aria-describedby={describedBy}>
    {selected.map((id, index) => <div className="asset-selection-item" key={id}>{assetByID[id] ? <AssetPreview asset={assetByID[id]} {...assetUrls(id)} compact /> : <Typography.Text type="secondary"><code>{id}</code></Typography.Text>}<Button size="small" danger disabled={disabled} aria-label={`移除素材 ${index + 1}`} onClick={() => onChange(multiple ? selected.filter((item) => item !== id) : null)}>移除</Button></div>)}
  </div>
  <Modal width={760} title={multiple ? '选择多个可用素材' : '选择可用素材'} open={open && !disabled} footer={null} onCancel={() => setOpen(false)}>
    <Space className="asset-picker-actions"><Button type="primary" disabled={!canUpload || disabled || (multiple && selected.length >= 50)} onClick={() => setUploadOpen(true)}>上传并选中</Button><Button onClick={() => assets.reload()}>刷新素材</Button></Space>
    <DataState loading={assets.loading} error={assets.error} empty={!assets.data?.items.length} retry={assets.reload}>
      <Table<Asset> size="small" rowKey="id" dataSource={assets.data?.items} pagination={false} scroll={{ x: 680 }} columns={[
        { title: '素材', width: 320, render: (_, asset) => <AssetPreview asset={asset} {...assetUrls(asset.id)} compact /> },
        { title: '素材 ID', dataIndex: 'id', render: (id: string) => <code>{id}</code> },
        { title: '选择', width: 90, render: (_, asset) => <Button type="primary" size="small" disabled={selected.includes(asset.id) || (multiple && selected.length >= 50)} onClick={() => choose(asset)}>{selected.includes(asset.id) ? '已选择' : '选择'}</Button> },
      ]} />
    </DataState>
    <Space className="pagination-actions"><Button disabled={cursors.length === 1} onClick={() => setCursors((values) => values.slice(0, -1))}>上一页</Button><Button disabled={!assets.data?.next_cursor} onClick={() => assets.data?.next_cursor && setCursors((values) => [...values, assets.data!.next_cursor!])}>下一页</Button></Space>
  </Modal><AssetUploadModal open={uploadOpen && !disabled} onCancel={() => setUploadOpen(false)} onUploaded={uploaded} disabled={disabled} /></div>
}
