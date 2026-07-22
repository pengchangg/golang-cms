import { Button, Card, Checkbox, DatePicker, Input, InputNumber, Select, Space, Switch, Typography } from 'antd'
import dayjs from 'dayjs'
import { lazy, Suspense } from 'react'

import type { ContentField, ReferencedAsset } from '../api/types'
import { ASSETS_ENABLED } from '../config'
import { fieldTypeMeta } from '../fieldTypes'
import { AssetPicker } from './AssetPicker'
import { RelationPicker } from './RelationPicker'

const JsonEditor = lazy(() => import('./JsonEditor').then((module) => ({ default: module.JsonEditor })))
const RichTextEditor = lazy(() => import('./RichTextEditor').then((module) => ({ default: module.RichTextEditor })))

function RepeatableGroupEditor({ field, value, onChange, disabled, canSelectAssets, canUploadAssets, referencedAssets, canViewModel, onFieldValidityChange, path, labelledBy, describedBy }: { field: ContentField; value: unknown; onChange: (value: Record<string, unknown>[]) => void; disabled: boolean; canSelectAssets: boolean; canUploadAssets: boolean; referencedAssets: Record<string, ReferencedAsset>; canViewModel: (modelId: string) => boolean; onFieldValidityChange?: (path: string, valid: boolean) => void; path: string; labelledBy: string; describedBy: string }) {
  const items = Array.isArray(value) ? value.filter((item): item is Record<string, unknown> => Boolean(item) && typeof item === 'object' && !Array.isArray(item)) : []
  return <div className="repeatable-group" role="group" aria-labelledby={labelledBy} aria-describedby={describedBy}><Space direction="vertical" size="middle" style={{ width: '100%' }}>{items.map((item, index) => <Card key={index} size="small" title={`第 ${index + 1} 项`} extra={<Button size="small" danger disabled={disabled} onClick={() => onChange(items.filter((_, itemIndex) => itemIndex !== index))}>删除</Button>}><DynamicContentForm fields={field.children} content={item} onChange={(next) => onChange(items.map((current, itemIndex) => itemIndex === index ? next : current))} disabled={disabled} canSelectAssets={canSelectAssets} canUploadAssets={canUploadAssets} referencedAssets={referencedAssets} canViewModel={canViewModel} onFieldValidityChange={onFieldValidityChange} path={`${path}/${index}`} /></Card>)}</Space><Button className="repeatable-add" disabled={disabled} onClick={() => onChange([...items, {}])}>添加一项</Button></div>
}

export function DynamicContentForm({ fields, content, onChange, disabled = false, canSelectAssets = true, canUploadAssets = false, referencedAssets = {}, canViewModel = () => true, onFieldValidityChange, path = '' }: { fields: ContentField[]; content: Record<string, unknown>; onChange: (value: Record<string, unknown>) => void; disabled?: boolean; canSelectAssets?: boolean; canUploadAssets?: boolean; referencedAssets?: Record<string, ReferencedAsset>; canViewModel?: (modelId: string) => boolean; onFieldValidityChange?: (path: string, valid: boolean) => void; path?: string }) {
  const update = (key: string, value: unknown) => onChange({ ...content, [key]: value })
  return <div className="dynamic-form">{fields.filter((field) => field.status === 'active').map((field) => {
    const value = content[field.key]
    const domID = `content-field-${`${path}/${field.id}`.replace(/[^A-Za-z0-9_-]/g, '-')}`
    const labelID = `${domID}-label`
    const metaID = `${domID}-meta`
    const descriptionID = field.description ? `${domID}-description` : undefined
    const describedBy = [metaID, descriptionID].filter(Boolean).join(' ')
    const accessibility = { 'aria-labelledby': labelID, 'aria-describedby': describedBy }
    let control
    if (field.type === 'single_media') {
      control = ASSETS_ENABLED ? <AssetPicker multiple={false} value={typeof value === 'string' ? value : null} onChange={(next) => update(field.key, next)} disabled={disabled || !canSelectAssets} canUpload={canUploadAssets} knownAssets={referencedAssets} labelledBy={labelID} describedBy={describedBy} /> : null
    } else if (field.type === 'multi_media') {
      control = ASSETS_ENABLED ? <AssetPicker multiple value={Array.isArray(value) ? [...new Set(value.filter((item): item is string => typeof item === 'string'))].slice(0, 50) : []} onChange={(next) => update(field.key, next)} disabled={disabled || !canSelectAssets} canUpload={canUploadAssets} knownAssets={referencedAssets} labelledBy={labelID} describedBy={describedBy} /> : null
    } else if (field.type === 'single_relation') {
      control = <RelationPicker key={`${field.constraints.target_model_id}:${canViewModel(field.constraints.target_model_id ?? '')}`} multiple={false} value={typeof value === 'string' ? value : null} onChange={(next) => update(field.key, next)} disabled={disabled} targetModelId={field.constraints.target_model_id} canViewTarget={Boolean(field.constraints.target_model_id && canViewModel(field.constraints.target_model_id))} labelledBy={labelID} describedBy={describedBy} />
    } else if (field.type === 'multi_relation') {
      control = <RelationPicker key={`${field.constraints.target_model_id}:${canViewModel(field.constraints.target_model_id ?? '')}`} multiple value={Array.isArray(value) ? [...new Set(value.filter((item): item is string => typeof item === 'string'))].slice(0, 50) : []} onChange={(next) => update(field.key, next)} disabled={disabled} targetModelId={field.constraints.target_model_id} canViewTarget={Boolean(field.constraints.target_model_id && canViewModel(field.constraints.target_model_id))} labelledBy={labelID} describedBy={describedBy} />
    } else if (field.type === 'single_line_text') control = <Input {...accessibility} value={typeof value === 'string' ? value : ''} onChange={(e) => update(field.key, e.target.value)} disabled={disabled} />
    else if (field.type === 'multi_line_text') control = <Input.TextArea {...accessibility} rows={4} value={typeof value === 'string' ? value : ''} onChange={(e) => update(field.key, e.target.value)} disabled={disabled} />
    else if (field.type === 'integer') control = <InputNumber {...accessibility} precision={0} value={typeof value === 'number' ? value : null} onChange={(next) => update(field.key, next)} disabled={disabled} />
    else if (field.type === 'decimal') control = <Input {...accessibility} value={typeof value === 'string' ? value : ''} onChange={(e) => update(field.key, e.target.value)} placeholder="规范十进制字符串，例如 19.90" disabled={disabled} />
    else if (field.type === 'boolean') control = <Switch {...accessibility} checked={value === true} onChange={(next) => update(field.key, next)} disabled={disabled} />
    else if (field.type === 'date' || field.type === 'datetime') control = <DatePicker {...accessibility} showTime={field.type === 'datetime'} value={typeof value === 'string' ? dayjs(value) : null} onChange={(next) => update(field.key, next ? (field.type === 'date' ? next.format('YYYY-MM-DD') : next.toISOString()) : null)} disabled={disabled} />
    else if (field.type === 'single_select') control = <Select {...accessibility} value={typeof value === 'string' ? value : undefined} options={field.constraints.enum_options} fieldNames={{ value: 'value', label: 'label' }} onChange={(next) => update(field.key, next)} disabled={disabled} />
    else if (field.type === 'multi_select') control = <Checkbox.Group {...accessibility} value={Array.isArray(value) ? value as string[] : []} options={(field.constraints.enum_options ?? []).map((item) => ({ label: item.label, value: item.value }))} onChange={(next) => update(field.key, next)} disabled={disabled} />
    else if (field.type === 'rich_text') control = <Suspense fallback={<div className="editor-loading">正在加载富文本编辑器</div>}><RichTextEditor label={field.display_name} value={value} onChange={(next) => update(field.key, next)} disabled={disabled} labelledBy={labelID} describedBy={describedBy} /></Suspense>
    else if (field.type === 'json') control = <Suspense fallback={<div className="editor-loading">正在加载 JSON 编辑器</div>}><JsonEditor label={field.display_name} value={value} onChange={(next) => update(field.key, next)} disabled={disabled} labelledBy={labelID} describedBy={describedBy} onValidityChange={(valid) => onFieldValidityChange?.(`${path}/${field.id}`, valid)} /></Suspense>
    else if (field.type === 'object') control = <div role="group" aria-labelledby={labelID} aria-describedby={describedBy}><DynamicContentForm fields={field.children} content={value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {}} onChange={(next) => update(field.key, next)} disabled={disabled} canSelectAssets={canSelectAssets} canUploadAssets={canUploadAssets} referencedAssets={referencedAssets} canViewModel={canViewModel} onFieldValidityChange={onFieldValidityChange} path={`${path}/${field.id}`} /></div>
    else if (field.type === 'repeatable_group') control = <RepeatableGroupEditor field={field} value={value} onChange={(next) => update(field.key, next)} disabled={disabled} canSelectAssets={canSelectAssets} canUploadAssets={canUploadAssets} referencedAssets={referencedAssets} canViewModel={canViewModel} onFieldValidityChange={onFieldValidityChange} path={`${path}/${field.id}`} labelledBy={labelID} describedBy={describedBy} />
    else control = null
    return <section className="dynamic-field" key={field.id}><div id={labelID}><strong>{field.display_name}</strong>{field.required ? <span className="required-mark">必填</span> : null}</div><Typography.Text id={metaID} type="secondary"><code>{field.key}</code> · {fieldTypeMeta[field.type].label}</Typography.Text>{control}{field.description ? <Typography.Text id={descriptionID} type="secondary">{field.description}</Typography.Text> : null}</section>
  })}</div>
}
