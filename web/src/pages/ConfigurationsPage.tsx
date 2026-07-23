import { Button, Form, Input, Modal, Space, Table, Tag, Typography, message } from 'antd'
import { useState } from 'react'
import { Link } from 'react-router-dom'

import { api, apiErrorMessage } from '../api/client'
import type { ConfigurationNamespace, Principal } from '../api/types'
import { hasSystemPermission } from '../auth/permissions'
import { DataState, PageHeader, useApiData } from '../components/Page'

interface NamespaceForm { namespace_key: string; display_name: string; description?: string }

export default function ConfigurationsPage({ principal }: { principal: Principal }) {
  const namespaces = useApiData(() => api.listConfigurationNamespaces(), [])
  const [editing, setEditing] = useState<ConfigurationNamespace | 'create'>()
  const [saving, setSaving] = useState(false)
  const [form] = Form.useForm<NamespaceForm>()
  const canCreate = hasSystemPermission(principal, 'configurations.create')
  const canUpdate = hasSystemPermission(principal, 'configurations.update')
  const canArchive = hasSystemPermission(principal, 'configurations.archive')

  function openEditor(value: ConfigurationNamespace | 'create') {
    setEditing(value)
    form.setFieldsValue(value === 'create' ? { namespace_key: '', display_name: '', description: '' } : value)
  }

  async function save(values: NamespaceForm) {
    setSaving(true)
    try {
      if (editing === 'create') await api.createConfigurationNamespace({ ...values, description: values.description ?? '' })
      else if (editing) await api.updateConfigurationNamespace(editing.id, { display_name: values.display_name, description: values.description ?? '' })
      message.success(editing === 'create' ? '命名空间已创建' : '命名空间已更新')
      setEditing(undefined)
      namespaces.reload()
    } catch (error) { message.error(apiErrorMessage(error, '保存命名空间失败')) }
    finally { setSaving(false) }
  }

  function archive(row: ConfigurationNamespace) {
    Modal.confirm({ title: `归档 ${row.display_name}？`, content: '必须先归档全部配置项并下线已发布值。', okText: '确认归档', okButtonProps: { danger: true }, onOk: async () => {
      try { await api.archiveConfigurationNamespace(row.id); message.success('命名空间已归档'); namespaces.reload() }
      catch (error) { message.error(apiErrorMessage(error, '归档命名空间失败')); throw error }
    } })
  }

  return <>
    <PageHeader eyebrow="运行配置" title="配置命名空间" description="以稳定 namespace key 组织客户端配置，定义与值工作流相互独立。" extra={<Button type="primary" disabled={!canCreate} onClick={() => openEditor('create')}>新建命名空间</Button>} />
    <DataState loading={namespaces.loading} error={namespaces.error} empty={!namespaces.data?.items.length} retry={namespaces.reload}>
      <Table<ConfigurationNamespace> rowKey="id" dataSource={namespaces.data?.items} pagination={false} scroll={{ x: 760 }} columns={[
        { title: '命名空间', render: (_, row) => <div><Link to={`/configurations/${row.id}`}><Typography.Text strong>{row.display_name}</Typography.Text></Link><br/><code>{row.namespace_key}</code></div> },
        { title: '说明', dataIndex: 'description', render: (value: string) => value || '无说明' },
        { title: '状态', dataIndex: 'status', render: (value) => <Tag color={value === 'active' ? 'green' : 'default'}>{value === 'active' ? '有效' : '已归档'}</Tag> },
        { title: '更新时间', dataIndex: 'updated_at', render: (value: string) => new Date(value).toLocaleString('zh-CN') },
        { title: '操作', fixed: 'right', render: (_, row) => <Space><Button size="small" disabled={!canUpdate || row.status === 'archived'} onClick={() => openEditor(row)}>编辑</Button><Button size="small" danger disabled={!canArchive || row.status === 'archived'} onClick={() => archive(row)}>归档</Button></Space> },
      ]} />
    </DataState>
    <Modal title={editing === 'create' ? '新建配置命名空间' : '编辑配置命名空间'} open={Boolean(editing)} onCancel={() => !saving && setEditing(undefined)} onOk={() => form.submit()} okButtonProps={{ loading: saving }}>
      <Form form={form} layout="vertical" onFinish={save}><Form.Item name="namespace_key" label="稳定标识" rules={[{ required: true }, { pattern: /^[a-z][a-z0-9_]{0,63}$/ }]}><Input disabled={editing !== 'create'} /></Form.Item><Form.Item name="display_name" label="显示名称" rules={[{ required: true }, { max: 120 }]}><Input /></Form.Item><Form.Item name="description" label="说明" rules={[{ max: 1000 }]}><Input.TextArea rows={3} /></Form.Item></Form>
    </Modal>
  </>
}
