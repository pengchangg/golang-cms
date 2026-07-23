import { Alert, Button, Modal, Segmented, Spin, Typography } from 'antd'
import { useEffect, useState, type ReactNode } from 'react'

import { api } from '../api/client'
import type { ContentEntry, ContentEntrySummary, ContentField, EntryListField, FieldType } from '../api/types'
import { AssetPreview } from './AssetPreview'
import { hydrateRichTextHTML } from './richText'

const PREVIEW_FIELD_TYPES = new Set<FieldType>([
  'rich_text', 'json', 'single_relation', 'multi_relation', 'object', 'repeatable_group',
])

export function isComplexPreviewField(type: FieldType) {
  return PREVIEW_FIELD_TYPES.has(type)
}

export function hasPreviewableValue(value: unknown, field: EntryListField): boolean {
  if (value === null || value === undefined || value === '') return false
  if (field.type === 'multi_relation' || field.type === 'repeatable_group') return Array.isArray(value) && value.length > 0
  if (field.type === 'object') return Boolean(value) && typeof value === 'object' && !Array.isArray(value)
  if (field.type === 'json') return true
  if (field.type === 'single_relation') return typeof value === 'string' && value !== ''
  if (field.type === 'rich_text') return typeof value === 'string' && value.trim() !== ''
  return true
}

export function displayValue(value: unknown, field: EntryListField): string {
  if (value === null || value === undefined || value === '') return '-'
  const labels = new Map(field.constraints.enum_options?.map((option) => [option.value, option.label]))
  let text: string
  if (field.type === 'single_select' && typeof value === 'string') text = labels.get(value) ?? value
  else if (field.type === 'multi_select' && Array.isArray(value)) text = value.map((item) => typeof item === 'string' ? labels.get(item) ?? item : String(item)).join('、')
  else if (typeof value === 'boolean') text = value ? '是' : '否'
  else if (typeof value === 'string' || typeof value === 'number') text = String(value)
  else text = JSON.stringify(value)
  return text.length > 80 ? `${text.slice(0, 77)}...` : text
}

export function contentFieldsToListFields(fields: ContentField[]): EntryListField[] {
  return fields.filter((field) => field.status === 'active').map((field) => ({
    key: field.key,
    display_name: field.display_name,
    type: field.type,
    constraints: {
      enum_options: field.constraints.enum_options,
      filterable: field.constraints.filterable,
      sortable: field.constraints.sortable,
      target_model_id: field.constraints.target_model_id,
    },
    children: contentFieldsToListFields(field.children),
  }))
}

function assetUrls(modelId: string, entryId: string, assetId: string) {
  const base = `/api/admin/v1/models/${encodeURIComponent(modelId)}/entries/${encodeURIComponent(entryId)}/assets/${encodeURIComponent(assetId)}`
  return { previewUrl: `${base}/preview`, downloadUrl: `${base}/download` }
}

function MediaCollection({ value, item, modelId, limit }: { value: unknown; item: ContentEntrySummary; modelId: string; limit?: number }) {
  const [open, setOpen] = useState(false)
  const ids = (Array.isArray(value) ? value : typeof value === 'string' ? [value] : []).filter((id): id is string => typeof id === 'string' && id !== '')
  if (!ids.length) return <span>-</span>
  const renderAsset = (id: string) => {
    const asset = item.referenced_assets[id]
    if (!asset) return <span key={id} className="asset-preview-unavailable" title={`素材 ${id} 的元数据不可用`}>素材不可用</span>
    const urls = assetUrls(modelId, item.id, asset.id)
    return <AssetPreview key={id} asset={asset} {...urls} compact />
  }
  const visible = limit ? ids.slice(0, limit) : ids
  return <div className="entry-media-collection">
    {visible.map(renderAsset)}
    {limit && ids.length > limit ? <><Button className="entry-media-more" type="link" onClick={() => setOpen(true)}>+{ids.length - limit} 查看全部</Button><Modal title="全部素材" open={open} footer={null} onCancel={() => setOpen(false)} destroyOnHidden><div className="entry-media-modal-list">{ids.map(renderAsset)}</div></Modal></> : null}
  </div>
}

function RichTextPreview({ value, item, modelId }: { value: unknown; item: ContentEntrySummary; modelId: string }) {
  const html = hydrateRichTextHTML(value, (assetId) => {
    if (!item.referenced_assets[assetId]) return undefined
    return assetUrls(modelId, item.id, assetId).previewUrl
  })
  if (!html.trim()) return <span>-</span>
  return <div className="entry-rich-text-preview" dangerouslySetInnerHTML={{ __html: html }} />
}

function serializeJSON(value: unknown) {
  return value == null ? 'null' : JSON.stringify(value, null, 2)
}

function JsonPreview({ value, label }: { value: unknown; label: string }) {
  return (
    <pre className="entry-json-preview" aria-label={`${label} JSON 预览`}>
      {serializeJSON(value)}
    </pre>
  )
}

function StructuredValue({ value, field, item, modelId, canViewModel }: { value: unknown; field: EntryListField; item: ContentEntrySummary; modelId: string; canViewModel: (modelId: string) => boolean }) {
  if (field.type === 'single_media' || field.type === 'multi_media') return <MediaCollection value={value} item={item} modelId={modelId} limit={3} />
  if (isComplexPreviewField(field.type)) {
    if (!hasPreviewableValue(value, field)) return <span>-</span>
    return <ComplexFieldPreview field={field} value={value} item={item} modelId={modelId} canViewModel={canViewModel} />
  }
  return <span title={displayValue(value, field)}>{displayValue(value, field)}</span>
}

function ObjectPreview({ value, field, item, modelId, canViewModel }: { value: unknown; field: EntryListField; item: ContentEntrySummary; modelId: string; canViewModel: (modelId: string) => boolean }) {
  if (field.type === 'object') {
    if (!value || typeof value !== 'object' || Array.isArray(value)) return <span>-</span>
    const record = value as Record<string, unknown>
    return <div className="entry-structured-value">{field.children.map((child) => <div className="entry-structured-row" key={child.key}><strong>{child.display_name}</strong><StructuredValue value={record[child.key]} field={child} item={item} modelId={modelId} canViewModel={canViewModel} /></div>)}</div>
  }
  if (!Array.isArray(value) || !value.length) return <span>-</span>
  return <div className="entry-structured-value">{value.map((group, index) => {
    const record = group && typeof group === 'object' && !Array.isArray(group) ? group as Record<string, unknown> : {}
    return <section className="entry-repeatable-item" key={index} aria-label={`${field.display_name}第 ${index + 1} 项`}><small>第 {index + 1} 项</small>{field.children.map((child) => <div className="entry-structured-row" key={child.key}><strong>{child.display_name}</strong><StructuredValue value={record[child.key]} field={child} item={item} modelId={modelId} canViewModel={canViewModel} /></div>)}</section>
  })}</div>
}

function EntryContentPreview({ fields, item, modelId, canViewModel }: { fields: EntryListField[]; item: ContentEntrySummary; modelId: string; canViewModel: (modelId: string) => boolean }) {
  return <div className="entry-content-preview">{fields.map((field) => <div className="entry-structured-row" key={field.key}><strong>{field.display_name}</strong><FieldValueDisplay field={field} value={item.current_draft_content[field.key]} item={item} modelId={modelId} canViewModel={canViewModel} /></div>)}</div>
}

type RelationLoadState =
  | { status: 'idle' }
  | { status: 'loading' }
  | { status: 'error'; message: string }
  | { status: 'ready'; fields: EntryListField[]; entries: Array<{ id: string; entry?: ContentEntry; error?: string }> }

function RelationPreview({ ids, targetModelId, canViewTarget, canViewModel }: { ids: string[]; targetModelId?: string; canViewTarget: boolean; canViewModel: (modelId: string) => boolean }) {
  const [state, setState] = useState<RelationLoadState>({ status: 'idle' })
  const [reloadKey, setReloadKey] = useState(0)
  const idsKey = ids.join('\0')

  useEffect(() => {
    const entryIDs = idsKey ? idsKey.split('\0') : []
    if (!targetModelId || !canViewTarget || !entryIDs.length) {
      setState({ status: 'idle' })
      return
    }
    let active = true
    setState({ status: 'loading' })
    void (async () => {
      try {
        const model = await api.getModel(targetModelId)
        const fields = contentFieldsToListFields(model.fields)
        const entries = await Promise.all(entryIDs.map(async (id) => {
          try {
            const entry = await api.getEntry(targetModelId, id)
            return { id, entry }
          } catch (cause) {
            return { id, error: cause instanceof Error ? cause.message : '加载失败' }
          }
        }))
        if (!active) return
        setState({ status: 'ready', fields, entries })
      } catch (cause) {
        if (!active) return
        setState({ status: 'error', message: cause instanceof Error ? cause.message : '加载失败' })
      }
    })()
    return () => { active = false }
  }, [idsKey, targetModelId, canViewTarget, reloadKey])

  if (!targetModelId) return <Alert type="warning" showIcon title="关联字段未配置目标模型" />
  if (!canViewTarget) {
    return <div className="entry-relation-ids"><Alert type="info" showIcon title="无目标模型内容查看权限" description="仅显示已保存的条目 ID。" /><ul>{ids.map((id) => <li key={id}><code>{id}</code></li>)}</ul></div>
  }
  if (state.status === 'loading' || state.status === 'idle') return <div className="entry-preview-loading"><Spin size="small" /> 正在加载关联内容</div>
  if (state.status === 'error') {
    return <div className="entry-preview-loading"><Typography.Text type="danger">{state.message}</Typography.Text><Button size="small" onClick={() => setReloadKey((value) => value + 1)}>重试</Button></div>
  }
  return <div className="entry-relation-preview">{state.entries.map(({ id, entry, error }) => (
    <section className="entry-repeatable-item" key={id} aria-label={`关联条目 ${id}`}>
      <small><code>{id}</code></small>
      {error ? <Typography.Text type="danger">{error}</Typography.Text> : entry ? <EntryContentPreview fields={state.fields} item={entry} modelId={targetModelId} canViewModel={canViewModel} /> : <span>-</span>}
    </section>
  ))}</div>
}

function previewBody(field: EntryListField, value: unknown, item: ContentEntrySummary, modelId: string, canViewModel: (modelId: string) => boolean): ReactNode {
  if (field.type === 'rich_text') return <RichTextPreview value={value} item={item} modelId={modelId} />
  if (field.type === 'json') return <JsonPreview value={value} label={field.display_name} />
  if (field.type === 'object' || field.type === 'repeatable_group') return <ObjectPreview value={value} field={field} item={item} modelId={modelId} canViewModel={canViewModel} />
  if (field.type === 'single_relation' || field.type === 'multi_relation') {
    const ids = (Array.isArray(value) ? value : typeof value === 'string' ? [value] : []).filter((id): id is string => typeof id === 'string' && id !== '')
    const targetModelId = field.constraints.target_model_id
    return <RelationPreview ids={ids} targetModelId={targetModelId} canViewTarget={Boolean(targetModelId && canViewModel(targetModelId))} canViewModel={canViewModel} />
  }
  return <span title={displayValue(value, field)}>{displayValue(value, field)}</span>
}

type PreviewSize = 'medium' | 'large' | 'fullscreen'

const PREVIEW_SIZE_STORAGE_KEY = 'cms.entry-field-preview.size'

const previewSizeOptions: Array<{ value: PreviewSize; label: string }> = [
  { value: 'medium', label: '标准' },
  { value: 'large', label: '较大' },
  { value: 'fullscreen', label: '全屏' },
]

const previewSizeWidth: Record<PreviewSize, string | number> = {
  medium: 720,
  large: 'min(1120px, 92vw)',
  fullscreen: '96vw',
}

function readStoredPreviewSize(): PreviewSize {
  try {
    const value = sessionStorage.getItem(PREVIEW_SIZE_STORAGE_KEY)
    if (value === 'medium' || value === 'large' || value === 'fullscreen') return value
  } catch {
    /* ignore unavailable storage */
  }
  return 'large'
}

function ComplexFieldPreview({ field, value, item, modelId, canViewModel }: { field: EntryListField; value: unknown; item: ContentEntrySummary; modelId: string; canViewModel: (modelId: string) => boolean }) {
  const [open, setOpen] = useState(false)
  const [size, setSize] = useState<PreviewSize>(readStoredPreviewSize)
  const count = field.type === 'multi_relation' && Array.isArray(value) ? value.filter((id) => typeof id === 'string' && id !== '').length : 0
  const label = count > 0 ? `预览 (${count})` : '预览'

  function changeSize(next: PreviewSize) {
    setSize(next)
    try {
      sessionStorage.setItem(PREVIEW_SIZE_STORAGE_KEY, next)
    } catch {
      /* ignore unavailable storage */
    }
  }

  return <>
    <Button type="link" className="entry-field-preview-trigger" onClick={() => setOpen(true)}>{label}</Button>
    <Modal
      title={(
        <div className="entry-field-preview-header">
          <span className="entry-field-preview-heading">{field.display_name}</span>
          <Segmented<PreviewSize> aria-label="预览窗口大小" size="small" value={size} options={previewSizeOptions} onChange={changeSize} />
        </div>
      )}
      open={open}
      footer={null}
      onCancel={() => setOpen(false)}
      destroyOnHidden
      width={previewSizeWidth[size]}
      centered={size !== 'fullscreen'}
      className={`entry-field-preview-modal is-size-${size}`}
      classNames={{ container: `entry-field-preview-modal is-size-${size}` }}
    >
      {open ? previewBody(field, value, item, modelId, canViewModel) : null}
    </Modal>
  </>
}

function FieldValueDisplay({ field, value, item, modelId, canViewModel }: { field: EntryListField; value: unknown; item: ContentEntrySummary; modelId: string; canViewModel: (modelId: string) => boolean }) {
  if (field.type === 'single_media' || field.type === 'multi_media') return <MediaCollection value={value} item={item} modelId={modelId} limit={3} />
  if (isComplexPreviewField(field.type)) {
    if (!hasPreviewableValue(value, field)) return <span>-</span>
    return <ComplexFieldPreview field={field} value={value} item={item} modelId={modelId} canViewModel={canViewModel} />
  }
  return <span title={displayValue(value, field)}>{displayValue(value, field)}</span>
}

export function EntryFieldValue({ field, item, modelId, canViewModel }: { field: EntryListField; item: ContentEntrySummary; modelId: string; canViewModel: (modelId: string) => boolean }) {
  return <FieldValueDisplay field={field} value={item.current_draft_content[field.key]} item={item} modelId={modelId} canViewModel={canViewModel} />
}
