import { Alert, Button, Select, Spin, Typography } from 'antd'
import { useEffect, useState, type ReactNode } from 'react'

import { api } from '../api/client'
import type { ContentEntrySummary, EntryListField } from '../api/types'

function entryLabel(entry: ContentEntrySummary, fields: EntryListField[]) {
  const titleField = fields.find((field) => field.type === 'single_line_text')
  const title = titleField ? entry.current_draft_content[titleField.key] : undefined
  return typeof title === 'string' && title.trim() ? `${title.trim()}（${entry.id}）` : entry.id
}

export function RelationPicker({ multiple, value, onChange, disabled, targetModelId, canViewTarget, labelledBy, describedBy }: { multiple: boolean; value: string | string[] | null; onChange: (value: string | string[] | null) => void; disabled: boolean; targetModelId?: string; canViewTarget: boolean; labelledBy?: string; describedBy?: string }) {
  const selected = multiple ? (Array.isArray(value) ? value : []) : (typeof value === 'string' ? value : undefined)
  const [items, setItems] = useState<ContentEntrySummary[]>([])
  const [fields, setFields] = useState<EntryListField[]>([])
  const [cursor, setCursor] = useState<string | null>()
  const [loading, setLoading] = useState(Boolean(targetModelId && canViewTarget))
  const [error, setError] = useState<unknown>()

  useEffect(() => {
    if (!targetModelId || !canViewTarget) return
    let active = true
    api.listEntries(targetModelId, { status: 'draft', limit: 100, cursor: undefined }).then((result) => {
      if (!active) return
      setItems(result.items)
      setFields(result.fields)
      setCursor(result.next_cursor)
      setLoading(false)
    }, (cause) => {
      if (!active) return
      setError(cause)
      setLoading(false)
    })
    return () => { active = false }
  }, [targetModelId, canViewTarget])

  async function loadMore(nextCursor?: string) {
    if (!targetModelId || !canViewTarget || loading) return
    setLoading(true)
    setError(undefined)
    try {
      const result = await api.listEntries(targetModelId, { status: 'draft', limit: 100, cursor: nextCursor })
      setItems((current) => [...current, ...result.items.filter((item) => !current.some((existing) => existing.id === item.id))])
      setFields(result.fields)
      setCursor(result.next_cursor)
    } catch (cause) {
      setError(cause)
    } finally {
      setLoading(false)
    }
  }

  const selectedIDs = Array.isArray(selected) ? selected : selected ? [selected] : []
  const options = [...items.map((item) => ({ value: item.id, label: entryLabel(item, fields) })), ...selectedIDs.filter((id) => !items.some((item) => item.id === id)).map((id) => ({ value: id, label: id }))]
  let footer: ReactNode
  if (loading) footer = <div className="relation-picker-state"><Spin size="small" /> 正在加载内容</div>
  else if (error) footer = <div className="relation-picker-state"><Typography.Text type="danger">内容加载失败</Typography.Text><Button size="small" onClick={() => void loadMore(cursor ?? undefined)}>重试</Button></div>
  else if (cursor) footer = <div className="relation-picker-state"><Button size="small" onClick={() => void loadMore(cursor)}>加载更多</Button></div>

  return <div className="relation-picker">
    {!targetModelId ? <Alert type="warning" showIcon title="关联字段未配置目标模型" /> : !canViewTarget ? <Alert type="info" showIcon title="无目标模型内容查看权限" description="现有条目 ID 会保留，但不能浏览或修改候选内容。" /> : null}
    <Select
      mode={multiple ? 'multiple' : undefined}
      aria-label={labelledBy ? undefined : multiple ? '选择多个关联内容' : '选择关联内容'}
      aria-labelledby={labelledBy}
      aria-describedby={describedBy}
      allowClear
      showSearch
      optionFilterProp="label"
      value={selected}
      options={options}
      maxCount={multiple ? 50 : undefined}
      placeholder={multiple ? '选择关联内容，最多 50 项' : '选择关联内容'}
      disabled={disabled || !targetModelId || !canViewTarget}
      loading={loading && !items.length}
      popupRender={(menu) => <>{menu}{footer}</>}
      onChange={(next) => onChange(multiple ? next as string[] : next ?? null)}
    />
  </div>
}
