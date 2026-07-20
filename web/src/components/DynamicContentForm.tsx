import { Button, Card, Checkbox, DatePicker, Input, InputNumber, Select, Space, Switch, Typography } from 'antd'
import dayjs from 'dayjs'
import { useState } from 'react'

import type { ContentField } from '../api/types'
import { ASSETS_ENABLED } from '../config'
import { fieldTypeMeta } from '../fieldTypes'
import { AssetPicker } from './AssetPicker'

function JsonEditor({ value, onChange, label, disabled }: { value: unknown; onChange: (value: unknown) => void; label: string; disabled: boolean }) {
  const serialized = value == null ? '' : JSON.stringify(value, null, 2)
  const [text, setText] = useState(serialized)
  return <Input.TextArea aria-label={`${label} JSON`} rows={5} value={text} disabled={disabled} onChange={(event) => {
    setText(event.target.value)
    try { onChange(event.target.value ? JSON.parse(event.target.value) : null) } catch { /* 编辑中的无效 JSON 不提交。 */ }
  }} />
}

function RepeatableGroupEditor({ field, value, onChange, disabled, canSelectAssets, canUploadAssets }: { field: ContentField; value: unknown; onChange: (value: Record<string, unknown>[]) => void; disabled: boolean; canSelectAssets: boolean; canUploadAssets: boolean }) {
  const items = Array.isArray(value) ? value.filter((item): item is Record<string, unknown> => Boolean(item) && typeof item === 'object' && !Array.isArray(item)) : []
  return <div className="repeatable-group"><Space direction="vertical" size="middle" style={{ width: '100%' }}>{items.map((item, index) => <Card key={index} size="small" title={`第 ${index + 1} 项`} extra={<Button size="small" danger disabled={disabled} onClick={() => onChange(items.filter((_, itemIndex) => itemIndex !== index))}>删除</Button>}><DynamicContentForm fields={field.children} content={item} onChange={(next) => onChange(items.map((current, itemIndex) => itemIndex === index ? next : current))} disabled={disabled} canSelectAssets={canSelectAssets} canUploadAssets={canUploadAssets} /></Card>)}</Space><Button className="repeatable-add" disabled={disabled} onClick={() => onChange([...items, {}])}>添加一项</Button></div>
}

export function DynamicContentForm({ fields, content, onChange, disabled = false, canSelectAssets = true, canUploadAssets = false }: { fields: ContentField[]; content: Record<string, unknown>; onChange: (value: Record<string, unknown>) => void; disabled?: boolean; canSelectAssets?: boolean; canUploadAssets?: boolean }) {
  const update = (key: string, value: unknown) => onChange({ ...content, [key]: value })
  return <div className="dynamic-form">{fields.filter((field) => field.status === 'active').map((field) => {
    const value = content[field.key]
    let control
    if (field.type === 'single_media') {
      control = ASSETS_ENABLED ? <AssetPicker multiple={false} value={typeof value === 'string' ? value : null} onChange={(next) => update(field.key, next)} disabled={disabled || !canSelectAssets} canUpload={canUploadAssets} /> : null
    } else if (field.type === 'multi_media') {
      control = ASSETS_ENABLED ? <AssetPicker multiple value={Array.isArray(value) ? [...new Set(value.filter((item): item is string => typeof item === 'string'))].slice(0, 50) : []} onChange={(next) => update(field.key, next)} disabled={disabled || !canSelectAssets} canUpload={canUploadAssets} /> : null
    } else if (field.type === 'single_relation') {
      control = <Input value={typeof value === 'string' ? value : ''} onChange={(event) => update(field.key, event.target.value || null)} placeholder={`目标条目 ID · ${field.constraints.target_model_id ?? '未配置模型'}`} disabled={disabled} />
    } else if (field.type === 'multi_relation') {
      control = <Select mode="tags" value={Array.isArray(value) ? value as string[] : []} onChange={(next) => update(field.key, next)} placeholder={`目标条目 ID，最多 50 项 · ${field.constraints.target_model_id ?? '未配置模型'}`} maxCount={50} tokenSeparators={[',', ' ']} disabled={disabled} />
    } else if (field.type === 'single_line_text') control = <Input value={typeof value === 'string' ? value : ''} onChange={(e) => update(field.key, e.target.value)} disabled={disabled} />
    else if (field.type === 'multi_line_text') control = <Input.TextArea rows={4} value={typeof value === 'string' ? value : ''} onChange={(e) => update(field.key, e.target.value)} disabled={disabled} />
    else if (field.type === 'integer') control = <InputNumber precision={0} value={typeof value === 'number' ? value : null} onChange={(next) => update(field.key, next)} disabled={disabled} />
    else if (field.type === 'decimal') control = <Input value={typeof value === 'string' ? value : ''} onChange={(e) => update(field.key, e.target.value)} placeholder="规范十进制字符串，例如 19.90" disabled={disabled} />
    else if (field.type === 'boolean') control = <Switch checked={value === true} onChange={(next) => update(field.key, next)} disabled={disabled} />
    else if (field.type === 'date' || field.type === 'datetime') control = <DatePicker showTime={field.type === 'datetime'} value={typeof value === 'string' ? dayjs(value) : null} onChange={(next) => update(field.key, next ? (field.type === 'date' ? next.format('YYYY-MM-DD') : next.toISOString()) : null)} disabled={disabled} />
    else if (field.type === 'single_select') control = <Select value={typeof value === 'string' ? value : undefined} options={field.constraints.enum_options} fieldNames={{ value: 'value', label: 'label' }} onChange={(next) => update(field.key, next)} disabled={disabled} />
    else if (field.type === 'multi_select') control = <Checkbox.Group value={Array.isArray(value) ? value as string[] : []} options={(field.constraints.enum_options ?? []).map((item) => ({ label: item.label, value: item.value }))} onChange={(next) => update(field.key, next)} disabled={disabled} />
    else if (field.type === 'object') control = <DynamicContentForm fields={field.children} content={value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {}} onChange={(next) => update(field.key, next)} disabled={disabled} canSelectAssets={canSelectAssets} canUploadAssets={canUploadAssets} />
    else if (field.type === 'repeatable_group') control = <RepeatableGroupEditor field={field} value={value} onChange={(next) => update(field.key, next)} disabled={disabled} canSelectAssets={canSelectAssets} canUploadAssets={canUploadAssets} />
    else control = <JsonEditor label={field.display_name} value={value} onChange={(next) => update(field.key, next)} disabled={disabled} />
    return <section className="dynamic-field" key={field.id}><label><strong>{field.display_name}</strong>{field.required ? <span className="required-mark">必填</span> : null}</label><Typography.Text type="secondary"><code>{field.key}</code> · {fieldTypeMeta[field.type].label}</Typography.Text>{control}{field.description ? <Typography.Text type="secondary">{field.description}</Typography.Text> : null}</section>
  })}</div>
}
