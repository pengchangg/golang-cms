import { Button, Modal, Space, Table, Tag, Typography } from 'antd'
import { useState } from 'react'

import { api } from '../api/client'
import type { Asset } from '../api/types'
import { DataState, useApiData } from './Page'

export function AssetPicker({ multiple, value, onChange, disabled }: { multiple: boolean; value: string | string[] | null; onChange: (value: string | string[] | null) => void; disabled?: boolean }) {
  const [open, setOpen] = useState(false)
  const [cursors, setCursors] = useState<Array<string | undefined>>([undefined])
  const assets = useApiData(() => open ? api.listAssets({ status: 'available', cursor: cursors.at(-1), limit: 20 }) : Promise.resolve({ items: [], next_cursor: null }), [open, cursors.at(-1)])
  const selected = multiple ? (Array.isArray(value) ? value : []) : (typeof value === 'string' ? [value] : [])

  function choose(asset: Asset) {
    if (!multiple) {
      onChange(asset.id)
      setOpen(false)
      return
    }
    if (selected.includes(asset.id) || selected.length >= 50) return
    onChange([...selected, asset.id])
  }

  return <div className="asset-picker"><Space wrap>
    <Button onClick={() => setOpen(true)} disabled={disabled}>{multiple ? '选择素材' : selected.length ? '更换素材' : '选择素材'}</Button>
    {selected.length ? <Typography.Text type="secondary">已选 {selected.length}{multiple ? ' / 50' : ''}</Typography.Text> : <Typography.Text type="secondary">未选择</Typography.Text>}
  </Space>
  <div className="asset-selection" aria-label="已选素材">
    {selected.map((id, index) => <Tag key={id} closable={!disabled} onClose={() => onChange(multiple ? selected.filter((item) => item !== id) : null)}><span>{multiple ? `${index + 1}. ` : ''}</span><code>{id}</code></Tag>)}
  </div>
  <Modal width={760} title={multiple ? '选择多个可用素材' : '选择可用素材'} open={open} footer={null} onCancel={() => setOpen(false)}>
    <DataState loading={assets.loading} error={assets.error} empty={!assets.data?.items.length} retry={assets.reload}>
      <Table<Asset> size="small" rowKey="id" dataSource={assets.data?.items} pagination={false} columns={[
        { title: '文件', dataIndex: 'filename', render: (name: string, asset) => <Button type="link" disabled={selected.includes(asset.id) || (multiple && selected.length >= 50)} onClick={() => choose(asset)}>{name}</Button> },
        { title: '类型', dataIndex: 'mime_type' },
        { title: '素材 ID', dataIndex: 'id', render: (id: string) => <code>{id}</code> },
      ]} />
    </DataState>
    <Space className="pagination-actions"><Button disabled={cursors.length === 1} onClick={() => setCursors((values) => values.slice(0, -1))}>上一页</Button><Button disabled={!assets.data?.next_cursor} onClick={() => assets.data?.next_cursor && setCursors((values) => [...values, assets.data!.next_cursor!])}>下一页</Button></Space>
  </Modal></div>
}
