import { ArrowDownOutlined, ArrowUpOutlined, DeleteOutlined, EditOutlined, HolderOutlined, PlusOutlined } from '@ant-design/icons'
import { DndContext, KeyboardSensor, PointerSensor, TouchSensor, closestCenter, useSensor, useSensors, type DragEndEvent } from '@dnd-kit/core'
import { SortableContext, sortableKeyboardCoordinates, useSortable, verticalListSortingStrategy } from '@dnd-kit/sortable'
import { CSS } from '@dnd-kit/utilities'
import { Alert, Button, Checkbox, Drawer, Form, Input, InputNumber, Modal, Select, Space, Switch, Tag, Tooltip, Typography, message } from 'antd'
import { useState, type ReactNode } from 'react'
import { useParams } from 'react-router-dom'

import { ApiError, api } from '../api/client'
import type { ContentField, FieldType, Principal, UpdateFieldOrderRequest } from '../api/types'
import { hasSystemPermission } from '../auth/permissions'
import { DataState, PageHeader, useApiData } from '../components/Page'
import { fieldTypeMeta, fieldTypeOptions } from '../fieldTypes'
import {
  activeFields, buildOrderRequest, complexDefaultTypes, fieldToForm, formToFieldInput, formToFieldPatch, isContainerType,
  isRelationType, isTextType, reorderActiveSiblings, replaceSiblings, serverForbiddenDefaultTypes, supportsRootProjection, validationFields, type FieldFormValues,
} from '../modelDesigner'
import { relationModelOptions } from '../relationModels'

type Editor = { field?: ContentField; parent?: ContentField; depth: number } | null

function errorText(error: unknown, fallback: string) {
  if (!(error instanceof ApiError)) return fallback
  const labels: Record<string, string> = {
    validation_failed: '字段配置不符合要求，请检查标红项目',
    key_conflict: '字段标识已存在，归档字段的标识也不能复用',
    field_type_locked: '模型已有内容，字段类型已锁定',
    field_projection_backfill_required: '模型已有内容，启用查询投影前需要先完成受控回填',
    target_model_self_relation: '关联目标不能是当前模型',
    resource_archived: '模型或字段已归档，不能继续修改',
    permission_denied: '没有执行此操作的权限',
  }
  return `${labels[error.code] ?? error.message ?? fallback}（请求 ID：${error.requestId}）`
}

export default function ModelDesignerPage({ principal }: { principal: Principal }) {
  const { modelId = '' } = useParams()
  const model = useApiData(() => api.getModel(modelId), [modelId])
  const targetModels = useApiData(() => api.listModels('active'), [])
  const [editor, setEditor] = useState<Editor>(null)
  const [saving, setSaving] = useState(false)
  const [reordering, setReordering] = useState(false)
  const [form] = Form.useForm<FieldFormValues>()
  const [messageApi, contextHolder] = message.useMessage()
  const canCreate = hasSystemPermission(principal, 'models.create')
  const canUpdate = hasSystemPermission(principal, 'models.update')
  const canArchive = hasSystemPermission(principal, 'models.archive')
  const disabled = !canUpdate || model.data?.status === 'archived'

  function openEditor(next: Exclude<Editor, null>) {
    if (saving || reordering) return
    form.resetFields()
    form.setFieldsValue(next.field ? fieldToForm(next.field) : {
      key: '', display_name: '', type: 'single_line_text', required: false, has_default: false,
      initial_children: [{ key: 'item', display_name: '子字段', type: 'single_line_text', required: false, has_default: false }],
    })
    setEditor(next)
  }

  async function saveField() {
    if (!editor || !model.data) return
    const values = await form.validateFields()
    setSaving(true)
    try {
      if (editor.field) {
        await api.updateField(modelId, editor.field.id, formToFieldPatch(values, editor.field, editor.depth === 0))
      } else if (editor.parent) {
        await api.createChildField(modelId, editor.parent.id, formToFieldInput(values, false))
      } else {
        await api.createField(modelId, formToFieldInput(values, true))
      }
      messageApi.success(editor.field ? '字段已更新' : '字段已添加')
      setEditor(null)
      model.reload(true)
    } catch (error) {
      if (error instanceof ApiError && error.code === 'validation_failed') form.setFields(validationFields(error.details, values) as Parameters<typeof form.setFields>[0])
      messageApi.error(errorText(error, '保存字段失败'))
    } finally {
      setSaving(false)
    }
  }

  function archive(field: ContentField) {
    if (saving || reordering) return
    Modal.confirm({
      title: `归档“${field.display_name}”？`,
      content: field.children.length ? '字段及其全部子字段将一并归档，且不能恢复。' : '字段将被归档，且不能恢复。',
      okText: '确认归档', okButtonProps: { danger: true }, cancelText: '取消',
      onOk: async () => {
        try { await api.archiveField(modelId, field.id); messageApi.success('字段已归档'); model.reload(true) }
        catch (error) { messageApi.error(errorText(error, '归档字段失败')); throw error }
      },
    })
  }

  async function reorder(parentId: string | null, siblings: ContentField[], request: UpdateFieldOrderRequest) {
    if (!model.data || reordering) return
    if (saving) return
    const snapshot = model.data
    setReordering(true)
    model.setData(replaceSiblings(snapshot, parentId, reorderActiveSiblings(siblings, request.field_ids)))
    try {
      await api.reorderFields(modelId, request)
    } catch (error) {
      if (error instanceof ApiError && error.code === 'field_order_conflict') {
        messageApi.warning('字段顺序已被其他人修改，已重新载入最新顺序')
      } else messageApi.error(errorText(error, '字段排序失败，正在重新载入服务端顺序'))
    } finally {
      setReordering(false)
      model.reload(true)
    }
  }

  const relationOptions = relationModelOptions(targetModels.data?.items ?? [], modelId)
  return <>
    {contextHolder}
    <PageHeader eyebrow="字段设计器" title={model.data?.display_name ?? '模型字段'} description="通过字段树组织内容结构；拖动手柄或使用方向操作调整同级顺序。" extra={<Button type="primary" icon={<PlusOutlined />} disabled={!canCreate || model.data?.status === 'archived' || saving || reordering} onClick={() => openEditor({ depth: 0 })}>添加根字段</Button>} />
    {!canUpdate ? <Alert className="editor-notice" type="info" showIcon title="你可以查看字段结构，但没有模型修改权限" /> : null}
    <DataState loading={model.loading} error={model.error} empty={!model.data?.fields.length} retry={model.reload}>
      {model.data ? <FieldList fields={model.data.fields} parentId={null} depth={0} disabled={disabled || saving || reordering} canArchive={canArchive && model.data.status === 'active'} onEdit={(field, depth) => openEditor({ field, depth })} onAddChild={(parent, depth) => openEditor({ parent, depth })} onArchive={archive} onReorder={reorder} /> : null}
    </DataState>
    <Drawer className="field-drawer" width={560} title={editor?.field ? `编辑字段 · ${editor.field.display_name}` : editor?.parent ? `添加子字段 · ${editor.parent.display_name}` : '添加根字段'} open={Boolean(editor)} maskClosable={!saving} keyboard={!saving} closable={!saving} onClose={() => { if (!saving) setEditor(null) }} extra={<Space><Button disabled={saving} onClick={() => setEditor(null)}>取消</Button><Button type="primary" loading={saving} disabled={reordering} onClick={saveField}>保存</Button></Space>}>
      <Form form={form} layout="vertical" requiredMark="optional"><FieldConfig form={form} path={[]} depth={editor?.depth ?? 0} editing={Boolean(editor?.field)} originalType={editor?.field?.type} originalDefaultValue={editor?.field?.default_value} relationOptions={relationOptions} relationError={targetModels.error} reloadRelations={targetModels.reload} /></Form>
    </Drawer>
  </>
}

function FieldList({ fields, parentId, depth, disabled, canArchive, onEdit, onAddChild, onArchive, onReorder }: {
  fields: ContentField[]; parentId: string | null; depth: number; disabled: boolean; canArchive: boolean
  onEdit: (field: ContentField, depth: number) => void; onAddChild: (field: ContentField, depth: number) => void
  onArchive: (field: ContentField) => void; onReorder: (parentId: string | null, siblings: ContentField[], request: UpdateFieldOrderRequest) => void
}) {
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 6 } }), useSensor(TouchSensor, { activationConstraint: { delay: 180, tolerance: 6 } }), useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }))
  const active = activeFields(fields)
  function move(field: ContentField, offset: number) {
    const index = active.findIndex((item) => item.id === field.id)
    const target = active[index + offset]
    if (target) { const request = buildOrderRequest(parentId, fields, field.id, target.id); if (request) onReorder(parentId, fields, request) }
  }
  function dragEnd(event: DragEndEvent) {
    const request = event.over ? buildOrderRequest(parentId, fields, String(event.active.id), String(event.over.id)) : null
    if (request) onReorder(parentId, fields, request)
  }
  return <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={dragEnd}><SortableContext items={active.map((field) => field.id)} strategy={verticalListSortingStrategy}><div className={depth ? 'field-children' : 'field-canvas'}>
    {fields.map((field) => <SortableField key={field.id} field={field} depth={depth} disabled={disabled || field.status === 'archived'} canArchive={canArchive} first={active[0]?.id === field.id} last={active.at(-1)?.id === field.id} onMove={move} onEdit={onEdit} onAddChild={onAddChild} onArchive={onArchive} onReorder={onReorder} />)}
  </div></SortableContext></DndContext>
}

/* dnd-kit 通过回调 ref 和监听属性连接 DOM，React 19 的 refs lint 无法识别该第三方模式。 */
/* eslint-disable react-hooks/refs */
function SortableField({ field, depth, disabled, canArchive, first, last, onMove, onEdit, onAddChild, onArchive, onReorder }: {
  field: ContentField; depth: number; disabled: boolean; canArchive: boolean; first: boolean; last: boolean
  onMove: (field: ContentField, offset: number) => void; onEdit: (field: ContentField, depth: number) => void; onAddChild: (field: ContentField, depth: number) => void
  onArchive: (field: ContentField) => void; onReorder: (parentId: string | null, siblings: ContentField[], request: UpdateFieldOrderRequest) => void
}) {
  const sortable = useSortable({ id: field.id, disabled })
  const style = { transform: CSS.Transform.toString(sortable.transform), transition: sortable.transition }
  const canAddChild = isContainerType(field.type) && depth < 2 && field.status === 'active'
  return <section ref={sortable.setNodeRef} style={style} className={`field-node${sortable.isDragging ? ' is-dragging' : ''}${field.status === 'archived' ? ' is-archived' : ''}`}>
    <div className="field-row">
      <Tooltip title={disabled ? '当前字段不可排序' : '拖动调整同级顺序'}><Button className="drag-handle" type="text" icon={<HolderOutlined />} aria-label={`拖动 ${field.display_name}`} disabled={disabled} {...sortable.attributes} {...sortable.listeners} /></Tooltip>
      <div className="field-summary"><Space size={[6, 4]} wrap><Typography.Text strong>{field.display_name}</Typography.Text><code>{field.key}</code><Tag>{fieldTypeMeta[field.type].label}</Tag>{field.required ? <Tag color="red">必填</Tag> : null}{field.status === 'archived' ? <Tag>已归档</Tag> : null}</Space>{field.description ? <Typography.Text type="secondary">{field.description}</Typography.Text> : null}</div>
      <Space size={2} className="field-actions">
        <Tooltip title="上移"><Button type="text" icon={<ArrowUpOutlined />} aria-label={`上移 ${field.display_name}`} disabled={disabled || first} onClick={() => onMove(field, -1)} /></Tooltip>
        <Tooltip title="下移"><Button type="text" icon={<ArrowDownOutlined />} aria-label={`下移 ${field.display_name}`} disabled={disabled || last} onClick={() => onMove(field, 1)} /></Tooltip>
        {canAddChild ? <Tooltip title="添加子字段"><Button type="text" icon={<PlusOutlined />} aria-label={`向 ${field.display_name} 添加子字段`} disabled={disabled} onClick={() => onAddChild(field, depth + 1)} /></Tooltip> : null}
        <Tooltip title="编辑"><Button type="text" icon={<EditOutlined />} aria-label={`编辑 ${field.display_name}`} disabled={disabled} onClick={() => onEdit(field, depth)} /></Tooltip>
        <Tooltip title="归档"><Button type="text" danger icon={<DeleteOutlined />} aria-label={`归档 ${field.display_name}`} disabled={disabled || field.status === 'archived' || !canArchive} onClick={() => onArchive(field)} /></Tooltip>
      </Space>
    </div>
    {field.children.length ? <FieldList fields={field.children} parentId={field.id} depth={depth + 1} disabled={disabled} canArchive={canArchive} onEdit={onEdit} onAddChild={onAddChild} onArchive={onArchive} onReorder={onReorder} /> : null}
  </section>
}
/* eslint-enable react-hooks/refs */

function FieldConfig({ form, path, depth, editing, originalType, originalDefaultValue, relationOptions, relationError, reloadRelations }: {
  form: ReturnType<typeof Form.useForm<FieldFormValues>>[0]; path: (string | number)[]; depth: number; editing: boolean; originalType?: FieldType
  originalDefaultValue?: unknown
  relationOptions: Array<{ value: string; label: string; searchText: string }>; relationError?: unknown; reloadRelations: () => void
}) {
  const name = (value: string) => [...path, value]
  const type = Form.useWatch(name('type'), form)
  const hasDefault = Form.useWatch(name('has_default'), form)
  const enumOptions = Form.useWatch(name('enum_options'), form) ?? []
  const needsInitialChildren = isContainerType(type) && (!editing || type !== originalType)
  return <>
    <Form.Item name={name('key')} label="稳定标识" rules={[{ required: true, message: '请输入稳定标识' }, { pattern: /^[a-z][a-z0-9_]{0,63}$/, message: '以小写字母开头，只能包含小写字母、数字和下划线' }]}><Input disabled={editing} placeholder="例如 article_title" /></Form.Item>
    {editing ? <Typography.Text className="field-key-notice" type="secondary">字段创建后，稳定标识不可修改。</Typography.Text> : null}
    <Form.Item name={name('display_name')} label="显示名称" rules={[{ required: true, message: '请输入显示名称' }, { max: 120 }]}><Input /></Form.Item>
    <Form.Item name={name('description')} label="说明" rules={[{ max: 1000 }]}><Input.TextArea rows={2} showCount maxLength={1000} /></Form.Item>
    <Form.Item name={name('type')} label="字段类型" rules={[{ required: true }]}><Select optionLabelProp="label" options={fieldTypeOptions.map((option) => ({ ...option, disabled: depth >= 2 && isContainerType(option.value), searchText: `${option.label} ${option.description} ${option.value}` }))} optionRender={(option) => <div className="field-type-option"><strong>{option.data.label}</strong><span>{option.data.description}</span></div>} showSearch optionFilterProp="searchText" /></Form.Item>
    <Form.Item name={name('required')} valuePropName="checked"><Checkbox>必填字段</Checkbox></Form.Item>
    {isTextType(type) ? <Space className="constraint-row" align="start"><Form.Item name={name('min_length')} label="最小长度"><InputNumber min={0} precision={0} /></Form.Item><Form.Item name={name('max_length')} label="最大长度" dependencies={[name('min_length')]} rules={[({ getFieldValue }) => ({ validator(_, value) { const min = getFieldValue(name('min_length')); return value === undefined || min === undefined || value >= min ? Promise.resolve() : Promise.reject(new Error('不能小于最小长度')) } })]}><InputNumber min={0} precision={0} /></Form.Item></Space> : null}
    {type === 'integer' || type === 'decimal' ? <Space className="constraint-row" align="start"><Form.Item name={name('minimum')} label="最小值" rules={[{ pattern: type === 'integer' ? /^-?(0|[1-9][0-9]*)$/ : /^-?(0|[1-9][0-9]*)(\.[0-9]+)?$/, message: '请输入不含指数的规范数值' }]}><Input /></Form.Item><Form.Item name={name('maximum')} label="最大值" rules={[{ pattern: type === 'integer' ? /^-?(0|[1-9][0-9]*)$/ : /^-?(0|[1-9][0-9]*)(\.[0-9]+)?$/, message: '请输入不含指数的规范数值' }]}><Input /></Form.Item></Space> : null}
    {type === 'single_select' || type === 'multi_select' ? <Form.List name={name('enum_options')} rules={[{ validator: async (_, options) => { if (!options?.length) throw new Error('至少添加一个选项') } }]}>{(fields, { add, remove }, { errors }) => <div className="enum-editor"><Typography.Text strong>枚举选项</Typography.Text>{fields.map((field, index) => { const existing = Boolean(enumOptions[index]?.existing); return <Space key={field.key} className="enum-row" align="start"><Form.Item name={[field.name, 'value']} rules={[{ required: true, message: '请输入 value' }, { max: 120 }]}><Input disabled={existing} placeholder="value" /></Form.Item><Form.Item name={[field.name, 'label']} rules={[{ required: true, message: '请输入标签' }, { max: 120 }]}><Input placeholder="显示标签" /></Form.Item><Button danger type="text" disabled={existing} onClick={() => remove(field.name)}>删除</Button></Space>})}<Button onClick={() => add({ value: '', label: '', existing: false })}>添加选项</Button><Form.ErrorList errors={errors} /></div>}</Form.List> : null}
    {isRelationType(type) ? <>{relationError ? <Alert type="error" showIcon title="目标模型加载失败" action={<Button size="small" onClick={reloadRelations}>重试</Button>} /> : null}<Form.Item name={name('target_model_id')} label="关联目标" rules={[{ required: true, message: '请选择关联目标模型' }]}><Select showSearch optionFilterProp="searchText" options={relationOptions} disabled={Boolean(relationError)} /></Form.Item></> : null}
    {depth === 0 && supportsRootProjection(type) ? <div className="projection-options"><Typography.Text strong>查询投影</Typography.Text><Typography.Text type="secondary">启用后可用于唯一性校验、筛选或排序。已有内容的模型可能需要先回填。</Typography.Text><Space wrap><Form.Item name={name('unique')} valuePropName="checked"><Checkbox>唯一</Checkbox></Form.Item><Form.Item name={name('filterable')} valuePropName="checked"><Checkbox>可筛选</Checkbox></Form.Item><Form.Item name={name('sortable')} valuePropName="checked"><Checkbox>可排序</Checkbox></Form.Item></Space></div> : null}
    {!serverForbiddenDefaultTypes.has(type) && !complexDefaultTypes.has(type) ? <div className="default-editor"><Form.Item name={name('has_default')} label="设置默认值" valuePropName="checked"><Switch /></Form.Item>{hasDefault ? <DefaultControl type={type} name={name} options={enumOptions} /> : null}</div> : serverForbiddenDefaultTypes.has(type) ? <Alert type="info" showIcon title="服务端不允许此类型设置非空默认值" /> : <Alert type="info" showIcon title={editing && type === originalType && originalDefaultValue != null ? '当前界面暂不支持编辑复杂默认值，保存时会原样保留' : '当前界面暂不支持编辑复杂默认值，新建或切换到此类型时将使用 null'} />}
    {needsInitialChildren ? <InitialChildren form={form} path={name('initial_children')} depth={depth + 1} relationOptions={relationOptions} relationError={relationError} reloadRelations={reloadRelations} /> : null}
  </>
}

function DefaultControl({ type, name, options }: { type?: FieldType; name: (value: string) => (string | number)[]; options: Array<{ value: string; label: string }> }) {
  let control: ReactNode = <Input />
  let field = 'default_text'
  if (type === 'integer') { field = 'default_number'; control = <InputNumber min={Number.MIN_SAFE_INTEGER} max={Number.MAX_SAFE_INTEGER} precision={0} /> }
  if (type === 'decimal') { field = 'default_decimal'; control = <Input /> }
  if (type === 'boolean') { field = 'default_boolean'; control = <Switch /> }
  if (type === 'date') { field = 'default_date'; control = <Input type="date" /> }
  if (type === 'datetime') { field = 'default_datetime'; control = <Input type="datetime-local" step={1} /> }
  if (type === 'single_select') { field = 'default_select'; control = <Select options={options} /> }
  if (type === 'multi_select') { field = 'default_multi_select'; control = <Select mode="multiple" options={options} /> }
  return <Form.Item name={name(field)} label="默认值" valuePropName={type === 'boolean' ? 'checked' : 'value'} rules={[{ required: type !== 'boolean', message: '请输入默认值' }, ...(type === 'integer' ? [{ validator: (_: unknown, value: number) => Number.isSafeInteger(value) ? Promise.resolve() : Promise.reject(new Error('请输入 JavaScript 安全整数')) }] : [])]}>{control}</Form.Item>
}

function InitialChildren({ form, path, depth, relationOptions, relationError, reloadRelations }: {
  form: ReturnType<typeof Form.useForm<FieldFormValues>>[0]; path: (string | number)[]; depth: number
  relationOptions: Array<{ value: string; label: string; searchText: string }>; relationError?: unknown; reloadRelations: () => void
}) {
  return <Form.List name={path} rules={[{ validator: async (_, children) => { if (!children?.length) throw new Error('容器至少需要一个子字段') } }]}>{(fields, { add, remove }, { errors }) => <div className="initial-children"><Typography.Title level={5}>初始子字段</Typography.Title>{fields.map((field, index) => <div className="initial-child" key={field.key}><div className="initial-child-heading"><Typography.Text strong>子字段 {index + 1}</Typography.Text><Button danger type="text" onClick={() => remove(field.name)}>移除</Button></div><FieldConfig form={form} path={[...path, field.name]} depth={depth} editing={false} relationOptions={relationOptions} relationError={relationError} reloadRelations={reloadRelations} /></div>)}<Button icon={<PlusOutlined />} onClick={() => add({ key: '', display_name: '', type: 'single_line_text', required: false, has_default: false })}>添加初始子字段</Button><Form.ErrorList errors={errors} /></div>}</Form.List>
}
