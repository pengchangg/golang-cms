import { Button, Checkbox, Form, Input, InputNumber, Modal, Select, Space, Table, Tag, Typography, message } from 'antd'
import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'

import { api, apiErrorMessage } from '../api/client'
import type { ConfigurationConstraints, ConfigurationItem, ConfigurationValueType, Principal } from '../api/types'
import { hasSystemPermission } from '../auth/permissions'
import { DataState, PageHeader, useApiData } from '../components/Page'

const valueTypeLabels: Record<ConfigurationValueType, string> = { string: '字符串', integer: '整数', decimal: '精确小数', boolean: '布尔值', json: 'JSON', single_asset: '单素材', multi_asset: '多素材', single_relation: '单关联', multi_relation: '多关联' }
interface ItemForm {
  item_key: string; display_name: string; description?: string; value_type: ConfigurationValueType; required?: boolean
  min_length?: number; max_length?: number; pattern?: string; string_enum?: string[]
  minimum?: string; maximum?: string; integer_enum?: string[]; scale?: number; decimal_enum?: string[]
  max_bytes?: number; allowed_mime_types?: string[]; max_size?: number; target_model_id?: string
}

function constraints(values: ItemForm): ConfigurationConstraints {
  const result: ConfigurationConstraints = { required: Boolean(values.required) }
  if (values.value_type === 'string') {
    if (values.min_length != null) result.min_length = values.min_length
    if (values.max_length != null) result.max_length = values.max_length
    if (values.pattern) result.pattern = values.pattern
    if (values.string_enum?.length) result.string_enum = values.string_enum
  }
  if (values.value_type === 'integer' || values.value_type === 'decimal') {
    if (values.minimum) result.minimum = values.minimum
    if (values.maximum) result.maximum = values.maximum
  }
  if (values.value_type === 'integer' && values.integer_enum?.length) result.integer_enum = values.integer_enum
  if (values.value_type === 'decimal') {
    if (values.scale != null) result.scale = values.scale
    if (values.decimal_enum?.length) result.decimal_enum = values.decimal_enum
  }
  if (values.value_type === 'json' && values.max_bytes != null) result.max_bytes = values.max_bytes
  if (values.value_type.endsWith('asset')) {
    if (values.allowed_mime_types?.length) result.allowed_mime_types = [...new Set(values.allowed_mime_types)].sort()
    if (values.max_size != null) result.max_size = values.max_size
  }
  if (values.value_type.endsWith('relation') && values.target_model_id) result.target_model_id = values.target_model_id
  return result
}

export default function ConfigurationNamespacePage({ principal }: { principal: Principal }) {
  const { namespaceId = '' } = useParams()
  const navigate = useNavigate()
  const namespace = useApiData(() => api.getConfigurationNamespace(namespaceId), [namespaceId])
  const items = useApiData(() => api.listConfigurationItems(namespaceId), [namespaceId])
  const canViewModels = hasSystemPermission(principal, 'models.view')
  const models = useApiData(() => canViewModels ? api.listModels('active') : Promise.resolve({ items: [] }), [canViewModels])
  const [editing, setEditing] = useState<ConfigurationItem | 'create'>()
  const [saving, setSaving] = useState(false)
  const [form] = Form.useForm<ItemForm>()
  const valueType = Form.useWatch('value_type', form)
  const canCreate = hasSystemPermission(principal, 'configurations.create')
  const canUpdate = hasSystemPermission(principal, 'configurations.update')
  const canArchive = hasSystemPermission(principal, 'configurations.archive')

  function openEditor(value: ConfigurationItem | 'create') {
    setEditing(value)
    form.resetFields()
    form.setFieldsValue(value === 'create' ? { item_key: '', display_name: '', description: '', value_type: 'string', required: false } : { ...value, ...value.constraints })
  }
  async function save(values: ItemForm) {
    setSaving(true)
    try {
      if (editing === 'create') await api.createConfigurationItem(namespaceId, { item_key: values.item_key, display_name: values.display_name, description: values.description ?? '', value_type: values.value_type, constraints: constraints(values) })
      else if (editing) await api.updateConfigurationItem(namespaceId, editing.id, { display_name: values.display_name, description: values.description ?? '', constraints: constraints(values) })
      message.success(editing === 'create' ? '配置定义已创建' : '配置定义已更新')
      setEditing(undefined); items.reload()
    } catch (error) { message.error(apiErrorMessage(error, '保存配置定义失败')) }
    finally { setSaving(false) }
  }
  function archive(row: ConfigurationItem) {
    Modal.confirm({ title: `归档 ${row.display_name}？`, content: '已发布配置必须先在值编辑页下线。', okText: '确认归档', okButtonProps: { danger: true }, onOk: async () => {
      try { await api.archiveConfigurationItem(namespaceId, row.id); message.success('配置项已归档'); items.reload() }
      catch (error) { message.error(apiErrorMessage(error, '归档配置项失败')); throw error }
    } })
  }

  return <>
    <PageHeader eyebrow="配置定义" title={namespace.data?.display_name ?? '配置命名空间'} description={namespace.data ? `${namespace.data.namespace_key} · ${namespace.data.description || '无说明'}` : '加载命名空间定义。'} extra={<Space wrap><Button onClick={() => navigate('/configurations')}>返回命名空间</Button><Button type="primary" disabled={!canCreate || namespace.data?.status === 'archived'} onClick={() => openEditor('create')}>新建配置项</Button></Space>} />
    <DataState loading={namespace.loading || items.loading} error={namespace.error ?? items.error} empty={!items.data?.items.length} retry={() => { namespace.reload(); items.reload() }}>
      <Table<ConfigurationItem> rowKey="id" dataSource={items.data?.items} pagination={false} scroll={{ x: 850 }} columns={[
        { title: '配置项', render: (_, row) => <div><Link to={`/configurations/${namespaceId}/items/${row.id}`}><Typography.Text strong>{row.display_name}</Typography.Text></Link><br/><code>{row.item_key}</code></div> },
        { title: '类型', dataIndex: 'value_type', render: (value: ConfigurationValueType) => valueTypeLabels[value] },
        { title: '约束', render: (_, row) => <Space wrap><Tag>{row.constraints.required ? '必填' : '可空'}</Tag>{row.constraints.target_model_id ? <Tag>{row.constraints.target_model_id}</Tag> : null}</Space> },
        { title: '状态', dataIndex: 'status', render: (value) => <Tag color={value === 'active' ? 'green' : 'default'}>{value === 'active' ? '有效' : '已归档'}</Tag> },
        { title: '操作', fixed: 'right', render: (_, row) => <Space><Button size="small" disabled={!canUpdate || row.status === 'archived'} onClick={() => openEditor(row)}>编辑定义</Button><Button size="small" danger disabled={!canArchive || row.status === 'archived'} onClick={() => archive(row)}>归档</Button></Space> },
      ]} />
    </DataState>
    <Modal width={680} title={editing === 'create' ? '新建配置项' : '编辑配置定义'} open={Boolean(editing)} onCancel={() => !saving && setEditing(undefined)} onOk={() => form.submit()} okButtonProps={{ loading: saving }}>
      <Form form={form} layout="vertical" onFinish={save}><Form.Item name="item_key" label="稳定标识" rules={[{ required: true }, { pattern: /^[a-z][a-z0-9_]{0,63}$/ }]}><Input disabled={editing !== 'create'} /></Form.Item><Form.Item name="display_name" label="显示名称" rules={[{ required: true }, { max: 120 }]}><Input /></Form.Item><Form.Item name="description" label="说明" rules={[{ max: 1000 }]}><Input.TextArea rows={2} /></Form.Item><Form.Item name="value_type" label="值类型" rules={[{ required: true }]}><Select disabled={editing !== 'create'} options={Object.entries(valueTypeLabels).map(([value, label]) => ({ value, label }))} /></Form.Item><Form.Item name="required" valuePropName="checked"><Checkbox>值不能为空</Checkbox></Form.Item>
        {valueType === 'string' ? <Space className="constraint-row"><Form.Item name="min_length" label="最短长度"><InputNumber min={0} precision={0} /></Form.Item><Form.Item name="max_length" label="最长长度"><InputNumber min={0} precision={0} /></Form.Item></Space> : null}
        {valueType === 'string' ? <><Form.Item name="pattern" label="匹配规则（RE2，最多 512 字节）"><Input maxLength={512} placeholder="例如 ^[a-z0-9_-]+$" /></Form.Item><Form.Item name="string_enum" label="可选字符串"><Select mode="tags" tokenSeparators={[',']} placeholder="输入后按回车，可填写多个" /></Form.Item></> : null}
        {valueType === 'integer' || valueType === 'decimal' ? <Space className="constraint-row"><Form.Item name="minimum" label="最小值"><Input /></Form.Item><Form.Item name="maximum" label="最大值"><Input /></Form.Item></Space> : null}
        {valueType === 'integer' ? <Form.Item name="integer_enum" label="可选整数（按字符串保存）"><Select mode="tags" tokenSeparators={[',']} placeholder="输入后按回车，例如 9007199254740993" /></Form.Item> : null}
        {valueType === 'decimal' ? <><Form.Item name="scale" label="最大小数位数"><InputNumber min={0} max={30} precision={0} /></Form.Item><Form.Item name="decimal_enum" label="可选小数（按字符串保存）"><Select mode="tags" tokenSeparators={[',']} placeholder="输入后按回车，例如 1.25" /></Form.Item></> : null}
        {valueType === 'json' ? <Form.Item name="max_bytes" label="最大 JSON 字节数"><InputNumber min={1} max={65536} precision={0} placeholder="默认 65536" /></Form.Item> : null}
        {valueType?.endsWith('asset') ? <><Form.Item name="allowed_mime_types" label="允许的 MIME 类型" rules={[{ validator: (_, values?: string[]) => values?.some((value) => !/^[a-z0-9!#$&^_.+%*-]+\/[a-z0-9!#$&^_.+%*-]+$/.test(value)) ? Promise.reject(new Error('请输入精确小写 MIME，例如 image/png')) : Promise.resolve() }]}><Select mode="tags" tokenSeparators={[',']} placeholder="例如 image/png，输入后按回车" /></Form.Item><Form.Item name="max_size" label="单个素材最大字节数"><InputNumber min={1} max={5368709120} precision={0} placeholder="最大 5368709120（5 GiB）" /></Form.Item></> : null}
        {valueType?.endsWith('relation') ? <Form.Item name="target_model_id" label="目标模型" rules={[{ required: true }]}><Select showSearch optionFilterProp="label" options={models.data?.items.map((model) => ({ value: model.id, label: `${model.display_name} · ${model.key}` }))} placeholder={canViewModels ? '选择 active 模型' : '需要 models.view 权限'} disabled={!canViewModels} /></Form.Item> : null}
      </Form>
    </Modal>
  </>
}
