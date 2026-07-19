import { Alert, Button, Card, Checkbox, Form, Input, Modal, Select, Space, Tag, Typography } from 'antd'
import { useState } from 'react'
import { useParams } from 'react-router-dom'

import { api } from '../api/client'
import type { ContentField, ContentFieldInput, FieldType, Principal } from '../api/types'
import { hasSystemPermission } from '../auth/permissions'
import { DataState, PageHeader, useApiData } from '../components/Page'

const fieldTypes = ['single_line_text','multi_line_text','rich_text','integer','decimal','boolean','date','datetime','single_select','multi_select','json','single_media','multi_media','single_relation','multi_relation','object','repeatable_group'].map((value) => ({ value: value as FieldType, label: value }))
const unsupported = new Set<FieldType>(['single_media','multi_media','single_relation','multi_relation'])
type FieldForm = ContentFieldInput & { options?: string; target_model_id?: string; children_json?: string }

export default function ModelDesignerPage({ principal }: { principal: Principal }) {
  const { modelId = '' } = useParams()
  const model = useApiData(() => api.getModel(modelId), [modelId])
  const [open, setOpen] = useState(false)
  const [form] = Form.useForm<FieldForm>()
  const type = Form.useWatch('type', form)
  const canUpdate = hasSystemPermission(principal, 'models.update')
  async function createField() {
    const value = await form.validateFields()
    const constraints = value.options
      ? { enum_options: value.options.split('\n').map((option) => option.trim()).filter(Boolean).map((option) => ({ value: option, label: option })) }
      : value.target_model_id ? { target_model_id: value.target_model_id } : {}
    let children: ContentFieldInput[] = []
    if (value.children_json) {
      try { children = JSON.parse(value.children_json) as ContentFieldInput[] }
      catch { form.setFields([{ name: 'children_json', errors: ['请输入合法 JSON 数组'] }]); return }
      if (!Array.isArray(children)) { form.setFields([{ name: 'children_json', errors: ['必须是子字段数组'] }]); return }
    }
    await api.createField(modelId, { key: value.key, display_name: value.display_name, description: value.description, type: value.type, required: value.required, default_value: null, constraints, children })
    setOpen(false); form.resetFields(); model.reload()
  }
  return <><PageHeader eyebrow="字段设计器" title={model.data?.display_name ?? '模型字段'} description="受控添加字段，嵌套对象沿用冻结字段 DTO，最大深度由服务端校验。" extra={<Button type="primary" disabled={!canUpdate || model.data?.status === 'archived'} onClick={() => setOpen(true)}>添加字段</Button>} /><DataState loading={model.loading} error={model.error} empty={!model.data?.fields.length} retry={model.reload}><div className="field-canvas">{model.data?.fields.map((field) => <FieldCard key={field.id} field={field} depth={0} />)}</div></DataState><Modal title="添加根字段" open={open} onCancel={() => setOpen(false)} onOk={createField}><Form form={form} layout="vertical"><Form.Item name="key" label="稳定标识" rules={[{ required: true }, { pattern: /^[a-z][a-z0-9_]{0,63}$/ }]}><Input /></Form.Item><Form.Item name="display_name" label="显示名称" rules={[{ required: true }]}><Input /></Form.Item><Form.Item name="type" label="字段类型" rules={[{ required: true }]}><Select options={fieldTypes} /></Form.Item><Form.Item name="required" valuePropName="checked"><Checkbox>必填</Checkbox></Form.Item>{type === 'single_select' || type === 'multi_select' ? <Form.Item name="options" label="选项值，每行一个" rules={[{ required: true }]}><Input.TextArea /></Form.Item> : null}{type === 'single_relation' || type === 'multi_relation' ? <Form.Item name="target_model_id" label="目标模型 ID" rules={[{ required: true }]}><Input /></Form.Item> : null}{type === 'object' || type === 'repeatable_group' ? <Form.Item name="children_json" label="子字段数组 JSON" rules={[{ required: true }]}><Input.TextArea rows={6} placeholder='[{"key":"name","display_name":"名称","type":"single_line_text"}]' /></Form.Item> : null}{type && unsupported.has(type) ? <Alert type="warning" showIcon title="媒体和关联字段本阶段不可编辑" description="可设计字段结构，但草稿内容只能缺省或保存 null。" /> : null}{type === 'rich_text' ? <Alert type="info" showIcon title="富文本使用结构化 JSON" description="本阶段不接收 HTML 字符串。" /> : null}</Form></Modal></>
}

function FieldCard({ field, depth }: { field: ContentField; depth: number }) {
  return <Card className="field-card" size="small"><Space wrap><Typography.Text strong>{field.display_name}</Typography.Text><code>{field.key}</code><Tag>{field.type}</Tag>{field.required ? <Tag color="red">必填</Tag> : null}{field.status === 'archived' ? <Tag>已归档</Tag> : null}</Space>{field.description ? <Typography.Paragraph type="secondary">{field.description}</Typography.Paragraph> : null}{field.children.length ? <div className="field-children" data-depth={depth + 1}>{field.children.map((child) => <FieldCard key={child.id} field={child} depth={depth + 1} />)}</div> : null}</Card>
}
