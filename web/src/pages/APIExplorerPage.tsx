import { ApiOutlined, ClearOutlined, DisconnectOutlined, SendOutlined } from '@ant-design/icons'
import { Alert, Button, Checkbox, Input, InputNumber, Radio, Select, Space, Spin, Tag, Typography, message } from 'antd'
import { useEffect, useRef, useState } from 'react'

import {
  contentAPICurl, requestContentAPI,
  type ContentAPIResponse, type PublishedEntryPage, type PublishedField, type PublishedModel, type PublishedModelSummary,
} from '../api/contentExplorer'
import { PageHeader } from '../components/Page'

type Operation = 'models' | 'model' | 'entries' | 'entry' | 'configuration' | 'configurationItem'
type FilterOperator = 'eq' | 'ne' | 'gt' | 'gte' | 'lt' | 'lte' | 'in'

const API_KEY_PATTERN = /^cmsk_[a-z2-7]{12}_[A-Za-z0-9_-]{43}$/
const operationLabels: Record<Operation, string> = {
  models: '发现模型', model: '模型定义', entries: '内容列表', entry: '内容详情',
  configuration: '配置命名空间', configurationItem: '配置单项',
}
const comparableTypes = new Set(['integer', 'decimal', 'date', 'datetime'])
const relationTypes = new Set(['single_relation', 'multi_relation'])

function parseFilterValue(field: PublishedField | undefined, operator: FilterOperator, value: string): unknown {
  const values = operator === 'in' ? value.split(',').map((item) => item.trim()).filter(Boolean) : [value.trim()]
  const parsed = values.map((item) => {
    if (field?.type === 'integer') {
      const number = Number(item)
      if (!Number.isInteger(number)) throw new Error('整数过滤值必须是整数')
      return number
    }
    if (field?.type === 'boolean') {
      if (item !== 'true' && item !== 'false') throw new Error('布尔过滤值必须是 true 或 false')
      return item === 'true'
    }
    return item
  })
  return operator === 'in' ? parsed : parsed[0]
}

function responseText(response: ContentAPIResponse) {
  if (response.data !== undefined) return JSON.stringify(response.data, null, 2)
  return response.text || (response.status === 304 ? '响应体为空，ETag 未变化。' : '响应体为空。')
}

export default function APIExplorerPage() {
  const [apiKey, setAPIKey] = useState('')
  const [models, setModels] = useState<PublishedModelSummary[]>([])
  const [model, setModel] = useState<PublishedModel>()
  const [operation, setOperation] = useState<Operation>('models')
  const [modelKey, setModelKey] = useState('')
  const [entryID, setEntryID] = useState('')
  const [namespaceKey, setNamespaceKey] = useState('')
  const [configurationKey, setConfigurationKey] = useState('')
  const [limit, setLimit] = useState(20)
  const [cursor, setCursor] = useState('')
  const [filterField, setFilterField] = useState('')
  const [filterOperator, setFilterOperator] = useState<FilterOperator>('eq')
  const [filterValue, setFilterValue] = useState('')
  const [relationField, setRelationField] = useState('')
  const [relationValue, setRelationValue] = useState('')
  const [sort, setSort] = useState<string[]>([])
  const [expand, setExpand] = useState<string[]>([])
  const [ifNoneMatch, setIfNoneMatch] = useState('')
  const [rawMode, setRawMode] = useState(false)
  const [rawQuery, setRawQuery] = useState('')
  const [response, setResponse] = useState<ContentAPIResponse>()
  const [networkError, setNetworkError] = useState('')
  const [requestPath, setRequestPath] = useState('/models')
  const [sending, setSending] = useState(false)
  const controllerRef = useRef<AbortController | undefined>(undefined)
  const generationRef = useRef(0)

  const fields = model?.fields ?? []
  const scalarFields = fields.filter((field) => field.constraints.filterable && !relationTypes.has(field.type))
  const relationFields = fields.filter((field) => relationTypes.has(field.type))
  const sortableFields = fields.filter((field) => field.constraints.sortable)
  const selectedFilter = scalarFields.find((field) => field.key === filterField)
  const operators: FilterOperator[] = selectedFilter && comparableTypes.has(selectedFilter.type)
    ? ['eq', 'ne', 'gt', 'gte', 'lt', 'lte', 'in']
    : ['eq', 'ne', 'in']

  useEffect(() => () => controllerRef.current?.abort(), [])

  function clearQuery() {
    setCursor(''); setFilterField(''); setFilterOperator('eq'); setFilterValue('')
    setRelationField(''); setRelationValue(''); setSort([]); setExpand([]); setRawQuery('')
  }

  function clearConnection() {
    controllerRef.current?.abort()
    generationRef.current += 1
    setAPIKey(''); setModels([]); setModel(undefined); setModelKey(''); setEntryID(''); setNamespaceKey(''); setConfigurationKey('')
    setResponse(undefined); setNetworkError(''); setRequestPath('/models'); setOperation('models'); setSending(false)
    clearQuery()
  }

  function buildPath(target = operation, nextCursor = cursor) {
    if (target === 'models') return '/models'
    if (target === 'configuration' || target === 'configurationItem') {
      if (!namespaceKey.trim()) throw new Error('请输入配置命名空间 key')
      const namespacePath = `/configurations/${encodeURIComponent(namespaceKey.trim())}`
      if (target === 'configurationItem') {
        if (!configurationKey.trim()) throw new Error('请输入配置项 key')
        return `${namespacePath}/${encodeURIComponent(configurationKey.trim())}`
      }
      return namespacePath
    }
    if (!modelKey) throw new Error('请先选择模型')
    const modelPath = `/models/${encodeURIComponent(modelKey)}`
    if (target === 'model') return modelPath
    if (target === 'entry') {
      if (!entryID.trim()) throw new Error('请输入内容 ID')
      const query = expand.length ? `?expand=${encodeURIComponent(expand.join(','))}` : ''
      return `${modelPath}/entries/${encodeURIComponent(entryID.trim())}${query}`
    }
    if (rawMode) return `${modelPath}/entries${rawQuery.trim() ? `?${rawQuery.trim().replace(/^\?/, '')}` : ''}`
    const query = new URLSearchParams()
    query.set('limit', String(limit))
    if (nextCursor) query.set('cursor', nextCursor)
    if (filterField && filterValue.trim()) query.set('filter', JSON.stringify({ [filterField]: { [filterOperator]: parseFilterValue(selectedFilter, filterOperator, filterValue) } }))
    if (relationField && relationValue.trim()) query.set('relation_filter', JSON.stringify({ [relationField]: { contains: relationValue.trim() } }))
    if (sort.length) query.set('sort', sort.join(','))
    if (expand.length) query.set('expand', expand.join(','))
    return `${modelPath}/entries?${query}`
  }

  async function send(target = operation, pathOverride?: string) {
    if (!apiKey.trim()) { message.error('请输入 API Key'); return }
    let path: string
    try { path = pathOverride ?? buildPath(target) }
    catch (error) { message.error(error instanceof Error ? error.message : '请求参数无效'); return }
    const generation = ++generationRef.current
    controllerRef.current?.abort()
    const controller = new AbortController()
    controllerRef.current = controller
    setSending(true); setNetworkError(''); setRequestPath(path)
    try {
      const result = await requestContentAPI(path, apiKey.trim(), { ifNoneMatch, signal: controller.signal })
      if (generation !== generationRef.current) return
      setResponse(result)
      if (target === 'models' && result.status === 200 && result.data && typeof result.data === 'object' && 'items' in result.data) {
        setModels((result.data as { items: PublishedModelSummary[] }).items)
      }
      if (target === 'model' && result.status === 200) setModel(result.data as PublishedModel)
    } catch (error) {
      if (controller.signal.aborted || generation !== generationRef.current) return
      setResponse(undefined)
      setNetworkError(error instanceof Error ? error.message : '网络请求失败')
    } finally {
      if (generation === generationRef.current) setSending(false)
    }
  }

  async function chooseModel(value: string) {
    setModelKey(value); setModel(undefined); setEntryID(''); clearQuery(); setOperation('model')
    await send('model', `/models/${encodeURIComponent(value)}`)
  }

  const currentPath = (() => { try { return buildPath() } catch { return requestPath } })()
  const statusColor = response && response.status >= 200 && response.status < 400 ? 'green' : 'red'
  const nextCursor = response?.data && typeof response.data === 'object' && 'next_cursor' in response.data
    ? (response.data as PublishedEntryPage).next_cursor : null

  return <>
    <PageHeader eyebrow="开发工具" title="客户端调试" description="使用真实 API Key 模拟客户端，只读取当前已发布内容和配置。密钥只保存在当前页面内存中。" />
    <Alert className="explorer-notice" type="warning" showIcon title="仅限受控开发环境" description="请求不会经过管理 API，也不会读取草稿。不要在共享屏幕、录屏或浏览器扩展可读取的环境中粘贴生产密钥。" />
    <section className="explorer-connection" aria-label="客户端连接">
      <div><Typography.Text className="eyebrow">01 / 凭据</Typography.Text><Typography.Title level={2}>连接内容 API</Typography.Title></div>
      <Input.Password aria-label="API Key" autoComplete="off" value={apiKey} onChange={(event) => setAPIKey(event.target.value)} placeholder="cmsk_..." status={apiKey && !API_KEY_PATTERN.test(apiKey) ? 'warning' : undefined} />
      <Space wrap>
        <Button type="primary" icon={<ApiOutlined />} loading={sending && operation === 'models'} disabled={!apiKey.trim()} onClick={() => { setOperation('models'); void send('models', '/models') }}>连接并发现模型</Button>
        <Button icon={<DisconnectOutlined />} disabled={!apiKey && !models.length} onClick={clearConnection}>清除连接</Button>
      </Space>
    </section>

    <div className="explorer-workspace">
      <section className="explorer-request" aria-label="请求构造器">
        <div className="explorer-section-heading"><div><Typography.Text className="eyebrow">02 / REQUEST</Typography.Text><Typography.Title level={2}>构造请求</Typography.Title></div><Tag color="green">GET</Tag></div>
        <Radio.Group aria-label="Content API 操作" optionType="button" buttonStyle="solid" value={operation} onChange={(event) => { setOperation(event.target.value); setCursor('') }} options={Object.entries(operationLabels).map(([value, label]) => ({ value, label }))} />
        {operation !== 'models' && operation !== 'configuration' && operation !== 'configurationItem' ? <label className="explorer-control"><span>内容模型</span><Select aria-label="内容模型" showSearch value={modelKey || undefined} placeholder={models.length ? '选择 API Key 可见模型' : '请先发现模型'} disabled={!models.length} onChange={(value) => void chooseModel(value)} options={models.map((item) => ({ value: item.key, label: `${item.display_name} · ${item.key}` }))} /></label> : null}
        {operation === 'entry' ? <label className="explorer-control"><span>内容 ID</span><Input aria-label="内容 ID" value={entryID} onChange={(event) => setEntryID(event.target.value)} placeholder="ent_..." /></label> : null}
        {operation === 'configuration' || operation === 'configurationItem' ? <label className="explorer-control"><span>配置命名空间 key</span><Input aria-label="配置命名空间 key" value={namespaceKey} onChange={(event) => setNamespaceKey(event.target.value)} placeholder="例如 website" /></label> : null}
        {operation === 'configurationItem' ? <label className="explorer-control"><span>配置项 key</span><Input aria-label="配置项 key" value={configurationKey} onChange={(event) => setConfigurationKey(event.target.value)} placeholder="例如 home.hero" /></label> : null}
        {(operation === 'entries' || operation === 'entry') && modelKey && !model ? <div className="explorer-inline-state"><Spin size="small" /> 正在读取模型定义</div> : null}
        {operation === 'entries' && model ? <>
          <Checkbox checked={rawMode} onChange={(event) => { setRawMode(event.target.checked); setCursor('') }}>原始查询参数模式</Checkbox>
          {rawMode ? <label className="explorer-control"><span>Query string</span><Input.TextArea aria-label="原始查询参数" autoSize={{ minRows: 3, maxRows: 7 }} value={rawQuery} onChange={(event) => setRawQuery(event.target.value)} placeholder={'limit=20&filter={"score":{"gte":80}}'} /></label> : <div className="explorer-query-grid">
            <label className="explorer-control"><span>每页数量</span><InputNumber aria-label="每页数量" min={1} max={100} value={limit} onChange={(value) => { setLimit(value ?? 20); setCursor('') }} /></label>
            <label className="explorer-control"><span>游标</span><Input aria-label="游标" value={cursor} onChange={(event) => setCursor(event.target.value)} placeholder="服务端返回的不透明游标" /></label>
            <label className="explorer-control"><span>过滤字段</span><Select aria-label="过滤字段" allowClear value={filterField || undefined} placeholder="filterable 字段" onChange={(value) => { setFilterField(value ?? ''); setFilterValue(''); setFilterOperator('eq'); setCursor('') }} options={scalarFields.map((field) => ({ value: field.key, label: `${field.display_name} · ${field.key}` }))} /></label>
            <label className="explorer-control"><span>过滤操作符</span><Select aria-label="过滤操作符" value={filterOperator} disabled={!filterField} onChange={(value) => { setFilterOperator(value); setCursor('') }} options={operators.map((value) => ({ value, label: value }))} /></label>
            <label className="explorer-control explorer-control-wide"><span>过滤值{filterOperator === 'in' ? '，逗号分隔' : ''}</span>{selectedFilter?.type === 'boolean' && filterOperator !== 'in' ? <Select aria-label="过滤值" value={filterValue || undefined} onChange={(value) => { setFilterValue(value); setCursor('') }} options={[{ value: 'true', label: 'true' }, { value: 'false', label: 'false' }]} /> : selectedFilter?.constraints.enum_options?.length && filterOperator !== 'in' ? <Select aria-label="过滤值" value={filterValue || undefined} onChange={(value) => { setFilterValue(value); setCursor('') }} options={selectedFilter.constraints.enum_options.map((option) => ({ value: option.value, label: `${option.label} · ${option.value}` }))} /> : <Input aria-label="过滤值" value={filterValue} disabled={!filterField} onChange={(event) => { setFilterValue(event.target.value); setCursor('') }} />}</label>
            <label className="explorer-control"><span>关联字段</span><Select aria-label="关联字段" allowClear value={relationField || undefined} placeholder="关联字段" onChange={(value) => { setRelationField(value ?? ''); setCursor('') }} options={relationFields.map((field) => ({ value: field.key, label: `${field.display_name} · ${field.key}` }))} /></label>
            <label className="explorer-control"><span>包含条目 ID</span><Input aria-label="关联条目 ID" value={relationValue} disabled={!relationField} onChange={(event) => { setRelationValue(event.target.value); setCursor('') }} placeholder="ent_..." /></label>
            <label className="explorer-control explorer-control-wide"><span>排序，最多 3 项</span><Select aria-label="排序" mode="multiple" maxCount={3} value={sort} onChange={(value) => { setSort(value); setCursor('') }} options={[
              { value: 'published_at', label: '发布时间升序' }, { value: '-published_at', label: '发布时间降序' }, { value: 'id', label: '内容 ID 升序' }, { value: '-id', label: '内容 ID 降序' },
              ...sortableFields.flatMap((field) => [{ value: field.key, label: `${field.display_name}升序` }, { value: `-${field.key}`, label: `${field.display_name}降序` }]),
            ]} /></label>
          </div>}
        </> : null}
        {(operation === 'entries' || operation === 'entry') && model ? <label className="explorer-control"><span>展开关联，最多 3 项</span><Select aria-label="展开关联" mode="multiple" maxCount={3} value={expand} onChange={(value) => { setExpand(value); setCursor('') }} options={relationFields.map((field) => ({ value: field.key, label: `${field.display_name} · ${field.key}` }))} /></label> : null}
        <label className="explorer-control"><span>If-None-Match</span><Input aria-label="If-None-Match" value={ifNoneMatch} onChange={(event) => setIfNoneMatch(event.target.value)} placeholder={'"sha256-..."'} /></label>
        <div className="explorer-request-preview"><span>GET</span><code>{currentPath}</code></div>
        <Space wrap>
          <Button type="primary" icon={<SendOutlined />} loading={sending} disabled={!apiKey.trim()} onClick={() => void send()}>发送请求</Button>
          <Button icon={<ClearOutlined />} onClick={() => { clearQuery(); setResponse(undefined); setNetworkError('') }}>清除参数</Button>
        </Space>
      </section>

      <section className="explorer-response" aria-label="响应查看器">
        <div className="explorer-section-heading"><div><Typography.Text className="eyebrow">03 / RESPONSE</Typography.Text><Typography.Title level={2}>检查响应</Typography.Title></div>{response ? <Space><Tag color={statusColor}>{response.status}</Tag><Typography.Text type="secondary">{Math.round(response.durationMs)} ms</Typography.Text></Space> : null}</div>
        {networkError ? <Alert type="error" showIcon title="请求未到达服务" description={networkError} /> : null}
        {!response && !networkError ? <div className="explorer-response-empty"><ApiOutlined /><span>发送请求后在这里查看真实响应</span></div> : null}
        {response ? <>
          <dl className="explorer-response-meta">
            <div><dt>HTTP</dt><dd>{response.status} {response.statusText}</dd></div>
            <div><dt>Request ID</dt><dd>{response.headers['x-request-id'] ?? '-'}</dd></div>
            <div><dt>ETag</dt><dd>{response.headers.etag ?? '-'}</dd></div>
            <div><dt>Cache-Control</dt><dd>{response.headers['cache-control'] ?? '-'}</dd></div>
            {response.headers['retry-after'] ? <div><dt>Retry-After</dt><dd>{response.headers['retry-after']}</dd></div> : null}
          </dl>
          <pre className="explorer-response-body" tabIndex={0}>{responseText(response)}</pre>
          {operation === 'entries' && nextCursor ? <Button onClick={() => { setCursor(nextCursor); void send('entries', buildPath('entries', nextCursor)) }}>使用 next_cursor 请求下一页</Button> : null}
        </> : null}
        <div className="explorer-curl"><Typography.Text className="eyebrow">CURL</Typography.Text><Typography.Paragraph copyable={{ text: contentAPICurl(currentPath, ifNoneMatch), tooltips: ['复制命令', '已复制'] }}><code>{contentAPICurl(currentPath, ifNoneMatch)}</code></Typography.Paragraph><Typography.Text type="secondary">命令使用 $API_KEY 占位符，不复制当前密钥。</Typography.Text></div>
      </section>
    </div>
  </>
}
