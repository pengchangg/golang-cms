import { Alert, Checkbox, DatePicker, Input, InputNumber, Select, Switch, Typography } from 'antd'
import dayjs from 'dayjs'
import { useState } from 'react'

import type { ContentField } from '../api/types'

const unsupported = new Set(['single_media', 'multi_media', 'single_relation', 'multi_relation'])

function JsonEditor({ value, onChange, label }: { value: unknown; onChange: (value: unknown) => void; label: string }) {
  const serialized = value == null ? '' : JSON.stringify(value, null, 2)
  const [text, setText] = useState(serialized)
  return <Input.TextArea aria-label={`${label} JSON`} rows={5} value={text} onChange={(event) => {
    setText(event.target.value)
    try { onChange(event.target.value ? JSON.parse(event.target.value) : null) } catch { /* 编辑中的无效 JSON 不提交。 */ }
  }} />
}

export function DynamicContentForm({ fields, content, onChange, disabled = false }: { fields: ContentField[]; content: Record<string, unknown>; onChange: (value: Record<string, unknown>) => void; disabled?: boolean }) {
  const update = (key: string, value: unknown) => onChange({ ...content, [key]: value })
  return <div className="dynamic-form">{fields.filter((field) => field.status === 'active').map((field) => {
    const value = content[field.key]
    let control
    if (unsupported.has(field.type)) {
      control = <Alert type="warning" showIcon title="本阶段不可编辑" description="媒体与关联字段需等待对应能力冻结和后端实现，仅允许留空。" />
    } else if (field.type === 'single_line_text') control = <Input value={typeof value === 'string' ? value : ''} onChange={(e) => update(field.key, e.target.value)} disabled={disabled} />
    else if (field.type === 'multi_line_text') control = <Input.TextArea rows={4} value={typeof value === 'string' ? value : ''} onChange={(e) => update(field.key, e.target.value)} disabled={disabled} />
    else if (field.type === 'integer') control = <InputNumber precision={0} value={typeof value === 'number' ? value : null} onChange={(next) => update(field.key, next)} disabled={disabled} />
    else if (field.type === 'decimal') control = <Input value={typeof value === 'string' ? value : ''} onChange={(e) => update(field.key, e.target.value)} placeholder="规范十进制字符串，例如 19.90" disabled={disabled} />
    else if (field.type === 'boolean') control = <Switch checked={value === true} onChange={(next) => update(field.key, next)} disabled={disabled} />
    else if (field.type === 'date' || field.type === 'datetime') control = <DatePicker showTime={field.type === 'datetime'} value={typeof value === 'string' ? dayjs(value) : null} onChange={(next) => update(field.key, next ? (field.type === 'date' ? next.format('YYYY-MM-DD') : next.toISOString()) : null)} disabled={disabled} />
    else if (field.type === 'single_select') control = <Select value={typeof value === 'string' ? value : undefined} options={field.constraints.enum_options} fieldNames={{ value: 'value', label: 'label' }} onChange={(next) => update(field.key, next)} disabled={disabled} />
    else if (field.type === 'multi_select') control = <Checkbox.Group value={Array.isArray(value) ? value as string[] : []} options={(field.constraints.enum_options ?? []).map((item) => ({ label: item.label, value: item.value }))} onChange={(next) => update(field.key, next)} disabled={disabled} />
    else if (field.type === 'object') control = <DynamicContentForm fields={field.children} content={value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {}} onChange={(next) => update(field.key, next)} disabled={disabled} />
    else control = <JsonEditor label={field.display_name} value={value} onChange={(next) => update(field.key, next)} />
    return <section className="dynamic-field" key={field.id}><label><strong>{field.display_name}</strong>{field.required ? <span className="required-mark">必填</span> : null}</label><Typography.Text type="secondary"><code>{field.key}</code> · {field.type}</Typography.Text>{control}{field.description ? <Typography.Text type="secondary">{field.description}</Typography.Text> : null}</section>
  })}</div>
}
