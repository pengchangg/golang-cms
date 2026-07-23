export type PublishedFieldType =
  | 'single_line_text' | 'multi_line_text' | 'rich_text' | 'integer' | 'decimal' | 'boolean'
  | 'date' | 'datetime' | 'single_select' | 'multi_select' | 'json' | 'single_media'
  | 'multi_media' | 'single_relation' | 'multi_relation' | 'object' | 'repeatable_group'

export interface PublishedField {
  id: string
  key: string
  display_name: string
  description: string
  type: PublishedFieldType
  required: boolean
  constraints: {
    enum_options?: Array<{ value: string; label: string }>
    target_model_key?: string
    unique: boolean
    filterable: boolean
    sortable: boolean
  }
  children: PublishedField[]
}

export interface PublishedModelSummary {
  id: string
  key: string
  display_name: string
  description: string
  updated_at: string
}

export interface PublishedModel extends PublishedModelSummary {
  fields: PublishedField[]
}

export interface PublishedEntry {
  id: string
  model_id: string
  model_key: string
  revision_id: string
  revision_number: number
  content: Record<string, unknown>
  expanded: Record<string, unknown>
  published_at: string
  updated_at: string
}

export interface PublishedEntryPage {
  items: PublishedEntry[]
  next_cursor: string | null
}

export interface ContentAPIResponse {
  status: number
  statusText: string
  durationMs: number
  headers: Record<string, string>
  text: string
  data?: unknown
}

const CONTENT_API_BASE = '/api/content/v1'
const allowedModelPath = /^\/models(?:\/[a-z][a-z0-9_]{0,63}(?:\/entries(?:\/[^/?#]+)?)?)?$/
const allowedConfigurationPath = /^\/configurations\/[a-z][a-z0-9_]{0,63}(?:\/[a-z][a-z0-9_.-]{0,119})?$/

export async function requestContentAPI(path: string, apiKey: string, options: { ifNoneMatch?: string; signal?: AbortSignal } = {}): Promise<ContentAPIResponse> {
  if (!path.startsWith('/') || path.startsWith('//') || path.includes('\\')) {
    throw new TypeError('Content API 路径必须是同源绝对路径')
  }
  let pathSegments: string[]
  try {
    pathSegments = path.split(/[?#]/, 1)[0].split('/').map((segment) => decodeURIComponent(segment))
  } catch (error) {
    throw new TypeError('Content API 路径编码无效', { cause: error })
  }
  if (pathSegments.some((segment) => segment === '.' || segment === '..')) {
    throw new TypeError('Content API 路径不能包含点路径段')
  }
  const url = new URL(`${CONTENT_API_BASE}${path}`, window.location.origin)
  const pathName = url.pathname.slice(CONTENT_API_BASE.length)
  if (url.origin !== window.location.origin || (!allowedModelPath.test(pathName) && !allowedConfigurationPath.test(pathName))) {
    throw new TypeError('只允许请求 Content API 的模型、内容和配置读取端点')
  }

  const headers = new Headers({ Authorization: `Bearer ${apiKey}` })
  if (options.ifNoneMatch?.trim()) headers.set('If-None-Match', options.ifNoneMatch.trim())
  const startedAt = performance.now()
  const response = await fetch(`${url.pathname}${url.search}`, {
    method: 'GET',
    headers,
    credentials: 'same-origin',
    cache: 'no-store',
    signal: options.signal,
  })
  const text = await response.text()
  let data: unknown
  if (text) {
    try { data = JSON.parse(text) }
    catch { data = undefined }
  }
  const responseHeaders: Record<string, string> = {}
  response.headers.forEach((value, name) => { responseHeaders[name] = value })
  return {
    status: response.status,
    statusText: response.statusText,
    durationMs: performance.now() - startedAt,
    headers: responseHeaders,
    text,
    data,
  }
}

export function contentAPICurl(path: string, ifNoneMatch?: string) {
  const safePath = path.replaceAll('$', '%24').replaceAll('`', '%60').replaceAll('\\', '%5C').replaceAll('"', '%22')
  const lines = ['curl --fail-with-body', '  --header "Authorization: Bearer $API_KEY"']
  if (ifNoneMatch?.trim()) lines.push(`  --header 'If-None-Match: ${ifNoneMatch.trim().replaceAll("'", "'\\''")}'`)
  lines.push(`  "$BASE_URL${CONTENT_API_BASE}${safePath}"`)
  return lines.join(' \\\n')
}
