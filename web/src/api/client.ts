import { authStore } from '../auth/store'
import type {
  APIKey, APIKeySecret, APIKeyStatus, Asset, AssetStatus, AssetUpload, AuditEvent, CaptchaChallenge, ContentEntry, ContentField, ContentFieldInput, ContentFieldPatch,
  ContentModel, ContentModelSummary, CreateAPIKeyRequest, CursorResponse, EntryListQuery,
  EntryListResponse, ErrorResponse, ExportCSVQuery, ModelPermission, Role, RotateAPIKeyRequest, SessionResponse,
  SMSChallenge, SystemPermission, UpdateFieldOrderRequest, User, UserStatus, UserSummary, WorkflowEvent, CreateAssetUploadRequest,
  ConfigurationConstraints, ConfigurationItem, ConfigurationItemValue, ConfigurationNamespace, ConfigurationRevision, ConfigurationValueType, ConfigurationWorkflowEvent, ConfigNamespacePermission,
} from './types'

const API_BASE = '/api/admin/v1'
const SAFE_METHODS = new Set(['GET', 'HEAD', 'OPTIONS'])
const BACKEND_SAFE_ASSET_MAX_BYTES = 100 * 1024 * 1024
const configuredAssetMaxBytes = Number(import.meta.env.VITE_ASSET_MAX_BYTES)

export const ASSET_MAX_BYTES = Number.isFinite(configuredAssetMaxBytes) && configuredAssetMaxBytes > 0
  ? Math.min(Math.floor(configuredAssetMaxBytes), BACKEND_SAFE_ASSET_MAX_BYTES)
  : BACKEND_SAFE_ASSET_MAX_BYTES
export const ASSET_MAX_LABEL = `${ASSET_MAX_BYTES / 1024 / 1024} MiB`

export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly requestId: string
  readonly details: ErrorResponse['error']['details']

  constructor(status: number, response: ErrorResponse) {
    super(response.error.message)
    this.name = 'ApiError'
    this.status = status
    this.code = response.error.code
    this.requestId = response.error.request_id
    this.details = response.error.details
  }
}

export function apiErrorMessage(error: unknown, fallback: string) {
  if (error instanceof ApiError) {
    const details = [...new Set(error.details.map((detail) => detail.message).filter(Boolean))]
    const message = details.length ? `${error.message}：${details.join('；')}` : error.message
    return `${message}（请求 ID：${error.requestId}）`
  }
  return error instanceof Error && error.message ? error.message : fallback
}

function isErrorResponse(value: unknown): value is ErrorResponse {
  if (!value || typeof value !== 'object' || !('error' in value)) return false
  const error = (value as { error: unknown }).error
  return Boolean(
    error &&
      typeof error === 'object' &&
      'code' in error &&
      'message' in error &&
      'request_id' in error,
  )
}

async function request<T>(path: string, init: RequestInit = {}, parse: (response: Response) => Promise<T> = (response) => response.json() as Promise<T>): Promise<T> {
  if (!path.startsWith('/') || path.startsWith('//')) {
    throw new TypeError('API 路径必须是同源绝对路径')
  }

  const method = (init.method ?? 'GET').toUpperCase()
  const authEpoch = authStore.getEpoch()
  const headers = new Headers(init.headers)
  if (init.body && !(init.body instanceof FormData) && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json')
  }
  if (!SAFE_METHODS.has(method)) {
    const csrfToken = authStore.getSnapshot().session?.csrf_token
    if (csrfToken) headers.set('X-CSRF-Token', csrfToken)
  }

  const response = await fetch(`${API_BASE}${path}`, {
    ...init,
    method,
    headers,
    credentials: 'same-origin',
  })

  if (!response.ok) {
    let payload: unknown
    try {
      payload = await response.json()
    } catch {
      payload = undefined
    }
    const requestId = response.headers.get('X-Request-ID') ?? 'unknown'
    const errorResponse: ErrorResponse = isErrorResponse(payload)
      ? payload
      : {
          error: {
            code: 'unexpected_response',
            message: `服务返回了未声明的错误状态（${response.status}）`,
            request_id: requestId,
            details: [],
          },
        }
    if (response.status === 401 && errorResponse.error.code === 'session_invalid') {
      authStore.clear(authEpoch)
    }
    throw new ApiError(response.status, errorResponse)
  }

  if (response.status === 204) return undefined as T
  return parse(response)
}

function queryString(values: Record<string, string | number | boolean | undefined | null>) {
  const query = new URLSearchParams()
  Object.entries(values).forEach(([key, value]) => {
    if (value !== undefined && value !== null && value !== '') query.set(key, String(value))
  })
  const result = query.toString()
  return result ? `?${result}` : ''
}

function json(method: string, body: unknown): RequestInit {
  return { method, body: JSON.stringify(body) }
}

function configurationDraftJSON(method: string, value: unknown, baseRevisionId?: string): RequestInit {
  if (typeof value !== 'bigint') return json(method, baseRevisionId ? { base_revision_id: baseRevisionId, value } : { value })
  const prefix = baseRevisionId ? `"base_revision_id":${JSON.stringify(baseRevisionId)},` : ''
  return { method, body: `{${prefix}"value":${value.toString()}}` }
}

async function parseConfigurationItemValue(response: Response): Promise<ConfigurationItemValue> {
  const text = await response.text()
  if (!/"value_type"\s*:\s*"integer"/.test(text)) return JSON.parse(text) as ConfigurationItemValue
  const parsed = JSON.parse(text.replace(/("value"\s*:\s*)(-?\d+)/g, '$1"$2"')) as ConfigurationItemValue
  if (parsed.current_draft_revision.value_type === 'integer' && typeof parsed.current_draft_revision.value === 'string') parsed.current_draft_revision.value = BigInt(parsed.current_draft_revision.value)
  if (parsed.current_published_revision?.value_type === 'integer' && typeof parsed.current_published_revision.value === 'string') parsed.current_published_revision.value = BigInt(parsed.current_published_revision.value)
  return parsed
}

async function parseConfigurationRevision(response: Response): Promise<ConfigurationRevision> {
  const text = await response.text()
  const parsed = JSON.parse(/"value_type"\s*:\s*"integer"/.test(text) ? text.replace(/("value"\s*:\s*)(-?\d+)/g, '$1"$2"') : text) as ConfigurationRevision
  if (parsed.value_type === 'integer' && typeof parsed.value === 'string') parsed.value = BigInt(parsed.value)
  return parsed
}

async function parseConfigurationRevisionList(response: Response): Promise<CursorResponse<ConfigurationRevision>> {
  const text = await response.text()
  const parsed = JSON.parse(text.replace(/("value_type"\s*:\s*"integer"(?:(?!"value"\s*:)[\s\S])*?"value"\s*:\s*)(-?\d+)/g, '$1"$2"')) as CursorResponse<ConfigurationRevision>
  parsed.items.forEach((revision) => { if (revision.value_type === 'integer' && typeof revision.value === 'string') revision.value = BigInt(revision.value) })
  return parsed
}

export const api = {
  getSession: () => request<SessionResponse>('/auth/session'),
  localLogin: (username: string, password: string) =>
    request<SessionResponse>('/auth/local/login', {
      method: 'POST',
      body: JSON.stringify({ username, password }),
    }),
  createCaptchaChallenge: () => request<CaptchaChallenge>('/auth/captcha/challenges', json('POST', {})),
  createSMSChallenge: (body: { phone: string; captcha_challenge_id: string; captcha_x: number; captcha_y: number }) =>
    request<SMSChallenge>('/auth/sms/challenges', json('POST', body)),
  verifySMSChallenge: (id: string, code: string) =>
    request<SessionResponse>(`/auth/sms/challenges/${encodeURIComponent(id)}/verify`, json('POST', { code })),
  logout: () => request<void>('/auth/logout', { method: 'POST' }),
  listUsers: (filters: { status?: string; auth_method?: string; query?: string; cursor?: string } = {}) =>
    request<CursorResponse<UserSummary>>(`/users${queryString(filters)}`),
  getUser: (id: string) => request<User>(`/users/${encodeURIComponent(id)}`),
  createUser: (body: { display_name: string; phone: string; role_ids: string[] }) => request<User>('/users', json('POST', body)),
  setUserStatus: (id: string, status: UserStatus) => request<User>(`/users/${encodeURIComponent(id)}`, json('PATCH', { status })),
  updateUserPhone: (id: string, phone: string) => request<User>(`/users/${encodeURIComponent(id)}/phone`, json('PATCH', { phone })),
  replaceUserRoles: (id: string, role_ids: string[]) => request<User>(`/users/${encodeURIComponent(id)}/roles`, json('PUT', { role_ids })),
  listRoles: () => request<{ items: Role[] }>('/roles'),
  createRole: (body: { key: string; display_name: string; description?: string }) => request<Role>('/roles', json('POST', body)),
  updateRoleConfigNamespacePermissions: (id: string, grants: Array<{ config_namespace_id: string; permissions: ConfigNamespacePermission[] }>) => request<Role>(`/roles/${encodeURIComponent(id)}`, json('PATCH', { config_namespace_permissions: grants })),
  replaceSystemPermissions: (id: string, permissions: SystemPermission[]) => request<Role>(`/roles/${encodeURIComponent(id)}/system-permissions`, json('PUT', { permissions })),
  replaceModelPermissions: (id: string, grants: Array<{ model_id: string; permissions: ModelPermission[] }>) => request<Role>(`/roles/${encodeURIComponent(id)}/model-permissions`, json('PUT', { grants })),
  listConfigurationNamespaces: (status?: string) => request<{ items: ConfigurationNamespace[] }>(`/configurations${queryString({ status })}`),
  getConfigurationNamespace: (id: string) => request<ConfigurationNamespace>(`/configurations/${encodeURIComponent(id)}`),
  createConfigurationNamespace: (body: { namespace_key: string; display_name: string; description: string }) => request<ConfigurationNamespace>('/configurations', json('POST', body)),
  updateConfigurationNamespace: (id: string, body: { display_name?: string; description?: string }) => request<ConfigurationNamespace>(`/configurations/${encodeURIComponent(id)}`, json('PATCH', body)),
  archiveConfigurationNamespace: (id: string) => request<void>(`/configurations/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  listConfigurationItems: (namespaceId: string, status?: string) => request<{ items: ConfigurationItem[] }>(`/configurations/${encodeURIComponent(namespaceId)}/items${queryString({ status })}`),
  getConfigurationItem: (namespaceId: string, itemId: string) => request<ConfigurationItem>(`/configurations/${encodeURIComponent(namespaceId)}/items/${encodeURIComponent(itemId)}`),
  createConfigurationItem: (namespaceId: string, body: { item_key: string; display_name: string; description: string; value_type: ConfigurationValueType; constraints: ConfigurationConstraints }) => request<ConfigurationItem>(`/configurations/${encodeURIComponent(namespaceId)}/items`, json('POST', body)),
  updateConfigurationItem: (namespaceId: string, itemId: string, body: { display_name?: string; description?: string; constraints?: ConfigurationConstraints }) => request<ConfigurationItem>(`/configurations/${encodeURIComponent(namespaceId)}/items/${encodeURIComponent(itemId)}`, json('PATCH', body)),
  archiveConfigurationItem: (namespaceId: string, itemId: string) => request<void>(`/configurations/${encodeURIComponent(namespaceId)}/items/${encodeURIComponent(itemId)}`, { method: 'DELETE' }),
  getConfigurationItemValue: (namespaceId: string, itemId: string) => request<ConfigurationItemValue>(`/configurations/${encodeURIComponent(namespaceId)}/items/${encodeURIComponent(itemId)}/value`, {}, parseConfigurationItemValue),
  listConfigurationRevisions: (namespaceId: string, itemId: string, cursor?: string) => request<CursorResponse<ConfigurationRevision>>(`/configurations/${encodeURIComponent(namespaceId)}/items/${encodeURIComponent(itemId)}/revisions${queryString({ cursor })}`, {}, parseConfigurationRevisionList),
  getConfigurationRevision: (namespaceId: string, itemId: string, revisionId: string) => request<ConfigurationRevision>(`/configurations/${encodeURIComponent(namespaceId)}/items/${encodeURIComponent(itemId)}/revisions/${encodeURIComponent(revisionId)}`, {}, parseConfigurationRevision),
  listConfigurationWorkflowEvents: (namespaceId: string, itemId: string, cursor?: string) => request<CursorResponse<ConfigurationWorkflowEvent>>(`/configurations/${encodeURIComponent(namespaceId)}/items/${encodeURIComponent(itemId)}/workflow-events${queryString({ cursor })}`),
  createConfigurationDraft: (namespaceId: string, itemId: string, value: unknown) => request<ConfigurationItemValue>(`/configurations/${encodeURIComponent(namespaceId)}/items/${encodeURIComponent(itemId)}/drafts`, configurationDraftJSON('POST', value), parseConfigurationItemValue),
  updateConfigurationDraft: (namespaceId: string, itemId: string, base_revision_id: string, value: unknown) => request<ConfigurationItemValue>(`/configurations/${encodeURIComponent(namespaceId)}/items/${encodeURIComponent(itemId)}/draft`, configurationDraftJSON('PATCH', value, base_revision_id), parseConfigurationItemValue),
  submitConfigurationItem: (namespaceId: string, itemId: string, revision_id: string) => request<ConfigurationItemValue>(`/configurations/${encodeURIComponent(namespaceId)}/items/${encodeURIComponent(itemId)}/submit`, json('POST', { revision_id }), parseConfigurationItemValue),
  approveConfigurationItem: (namespaceId: string, itemId: string, revision_id: string) => request<ConfigurationItemValue>(`/configurations/${encodeURIComponent(namespaceId)}/items/${encodeURIComponent(itemId)}/approve`, json('POST', { revision_id }), parseConfigurationItemValue),
  rejectConfigurationItem: (namespaceId: string, itemId: string, revision_id: string, reason: string) => request<ConfigurationItemValue>(`/configurations/${encodeURIComponent(namespaceId)}/items/${encodeURIComponent(itemId)}/reject`, json('POST', { revision_id, reason }), parseConfigurationItemValue),
  unpublishConfigurationItem: (namespaceId: string, itemId: string, revision_id: string) => request<ConfigurationItemValue>(`/configurations/${encodeURIComponent(namespaceId)}/items/${encodeURIComponent(itemId)}/unpublish`, json('POST', { revision_id }), parseConfigurationItemValue),
  listModels: (status?: string) => request<{ items: ContentModelSummary[] }>(`/models${queryString({ status })}`),
  getModel: (id: string) => request<ContentModel>(`/models/${encodeURIComponent(id)}`),
  createModel: (body: { key: string; display_name: string; description?: string }) => request<ContentModel>('/models', json('POST', body)),
  listFields: (modelId: string) => request<{ items: ContentField[] }>(`/models/${encodeURIComponent(modelId)}/fields`),
  getField: (modelId: string, fieldId: string) => request<ContentField>(`/models/${encodeURIComponent(modelId)}/fields/${encodeURIComponent(fieldId)}`),
  createField: (modelId: string, body: ContentFieldInput) => request<ContentField>(`/models/${encodeURIComponent(modelId)}/fields`, json('POST', body)),
  createChildField: (modelId: string, parentId: string, body: ContentFieldInput) => request<ContentField>(`/models/${encodeURIComponent(modelId)}/fields/${encodeURIComponent(parentId)}/children`, json('POST', body)),
  updateField: (modelId: string, fieldId: string, body: ContentFieldPatch) => request<ContentField>(`/models/${encodeURIComponent(modelId)}/fields/${encodeURIComponent(fieldId)}`, json('PATCH', body)),
  archiveField: (modelId: string, fieldId: string) => request<void>(`/models/${encodeURIComponent(modelId)}/fields/${encodeURIComponent(fieldId)}`, { method: 'DELETE' }),
  reorderFields: (modelId: string, body: UpdateFieldOrderRequest) => request<void>(`/models/${encodeURIComponent(modelId)}/fields/order`, json('PUT', body)),
  listEntries: (modelId: string, query: EntryListQuery = {}) => request<EntryListResponse>(`/models/${encodeURIComponent(modelId)}/entries${queryString({ ...query })}`),
  getEntry: (modelId: string, entryId: string) => request<ContentEntry>(`/models/${encodeURIComponent(modelId)}/entries/${encodeURIComponent(entryId)}`),
  createEntry: (modelId: string, content: Record<string, unknown>) => request<ContentEntry>(`/models/${encodeURIComponent(modelId)}/entries`, json('POST', { content })),
  updateEntry: (modelId: string, entryId: string, base_revision_id: string, content: Record<string, unknown>) => request<ContentEntry>(`/models/${encodeURIComponent(modelId)}/entries/${encodeURIComponent(entryId)}`, json('PATCH', { base_revision_id, content })),
  submitEntry: (modelId: string, entryId: string, revision_id: string) => request<ContentEntry>(`/models/${encodeURIComponent(modelId)}/entries/${encodeURIComponent(entryId)}/submit`, json('POST', { revision_id })),
  approveEntry: (modelId: string, entryId: string, revision_id: string) => request<ContentEntry>(`/models/${encodeURIComponent(modelId)}/entries/${encodeURIComponent(entryId)}/approve`, json('POST', { revision_id })),
  rejectEntry: (modelId: string, entryId: string, revision_id: string, reason: string) => request<ContentEntry>(`/models/${encodeURIComponent(modelId)}/entries/${encodeURIComponent(entryId)}/reject`, json('POST', { revision_id, reason })),
  unpublishEntry: (modelId: string, entryId: string, revision_id: string) => request<ContentEntry>(`/models/${encodeURIComponent(modelId)}/entries/${encodeURIComponent(entryId)}/unpublish`, json('POST', { revision_id })),
  listWorkflowEvents: (modelId: string, entryId: string, cursor?: string) => request<CursorResponse<WorkflowEvent>>(`/models/${encodeURIComponent(modelId)}/entries/${encodeURIComponent(entryId)}/workflow-events${queryString({ cursor })}`),
  listAPIKeys: (status?: APIKeyStatus, cursor?: string) => request<CursorResponse<APIKey>>(`/api-keys${queryString({ status, cursor })}`),
  createAPIKey: (body: CreateAPIKeyRequest) => request<APIKeySecret>('/api-keys', { ...json('POST', body), cache: 'no-store' }),
  revokeAPIKey: (id: string) => request<void>(`/api-keys/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  rotateAPIKey: (id: string, body: RotateAPIKeyRequest = {}) => request<APIKeySecret>(`/api-keys/${encodeURIComponent(id)}/rotate`, { ...json('POST', body), cache: 'no-store' }),
  listAuditEvents: (filters: Record<string, string | undefined>) => request<CursorResponse<AuditEvent>>(`/audit/events${queryString(filters)}`),
  listAssets: (filters: { status?: AssetStatus; mime_type?: string; kind?: 'image' | 'audio' | 'video'; limit?: number; cursor?: string } = {}) => request<CursorResponse<Asset>>(`/assets${queryString(filters)}`),
  getAsset: (id: string) => request<Asset>(`/assets/${encodeURIComponent(id)}`),
  createAssetUpload: (body: CreateAssetUploadRequest) => request<AssetUpload>('/assets/uploads', { ...json('POST', body), cache: 'no-store' }),
  confirmAssetUpload: (id: string) => request<Asset>(`/assets/${encodeURIComponent(id)}/confirm`, json('POST', {})),
  discardQuarantinedAsset: (id: string) => request<void>(`/assets/${encodeURIComponent(id)}/quarantine`, { method: 'DELETE' }),
  updateAsset: (id: string, filename: string) => request<Asset>(`/assets/${encodeURIComponent(id)}`, json('PATCH', { filename })),
  archiveAsset: (id: string) => request<void>(`/assets/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  importCSV: (modelId: string, file: File) => {
    const body = new FormData()
    body.append('file', file)
    return request<{ imported: number }>(`/models/${encodeURIComponent(modelId)}/imports`, { method: 'POST', body })
  },
  exportCSV: (modelId: string, query: ExportCSVQuery) => request<Blob>(`/models/${encodeURIComponent(modelId)}/exports.csv${queryString({ ...query })}`, {}, (response) => response.blob()),
}

export async function putSignedUpload(upload: { method: 'PUT'; url: string; headers: Record<string, string> }, file: Blob) {
  const response = await fetch(upload.url, { method: upload.method, headers: upload.headers, body: file })
  if (!response.ok) throw new Error(`对象存储上传失败（${response.status}）`)
}

export async function uploadAssetFile(file: File) {
  const sha256 = await hashUploadFile(file)
  const signed = await api.createAssetUpload({ filename: file.name, mime_type: file.type, size: file.size, sha256 })
  await putSignedUpload(signed.upload, file)
  const asset = await api.confirmAssetUpload(signed.asset.id)
  if (asset.status !== 'available') throw new Error('素材确认后仍不可用')
  return asset
}

export async function hashUploadFile(file: File) {
  if (file.size > ASSET_MAX_BYTES) {
    throw new Error(`文件“${file.name}”超过 ${ASSET_MAX_LABEL} 上限，已拒绝读取。浏览器 SHA-256 不支持流式计算，请选择更小的文件。`)
  }
  const digest = await crypto.subtle.digest('SHA-256', await file.arrayBuffer())
  return [...new Uint8Array(digest)].map((byte) => byte.toString(16).padStart(2, '0')).join('')
}

export function adminDownloadUrl(path: string) {
  if (!path.startsWith('/') || path.startsWith('//')) throw new TypeError('下载路径必须是同源绝对路径')
  return `${API_BASE}${path}`
}

export function safeReturnTo(value: unknown) {
  if (typeof value !== 'string' || !value.startsWith('/') || value.startsWith('//') || value.includes('\\')) {
    return null
  }
  try {
    const url = new URL(value, window.location.origin)
    if (url.origin !== window.location.origin) return null
    return `${url.pathname}${url.search}${url.hash}`
  } catch {
    return null
  }
}
