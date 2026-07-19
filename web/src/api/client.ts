import { authStore } from '../auth/store'
import type {
  AuditEvent, ContentEntry, ContentEntrySummary, ContentField, ContentFieldInput,
  ContentModel, ContentModelSummary, CursorResponse, ErrorResponse, ModelPermission,
  Role, SessionResponse, SystemPermission, User, UserStatus, UserSummary,
} from './types'

const API_BASE = '/api/admin/v1'
const SAFE_METHODS = new Set(['GET', 'HEAD', 'OPTIONS'])

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

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  if (!path.startsWith('/') || path.startsWith('//')) {
    throw new TypeError('API 路径必须是同源绝对路径')
  }

  const method = (init.method ?? 'GET').toUpperCase()
  const headers = new Headers(init.headers)
  if (init.body && !headers.has('Content-Type')) {
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
      authStore.clear()
    }
    throw new ApiError(response.status, errorResponse)
  }

  if (response.status === 204) return undefined as T
  return response.json() as Promise<T>
}

function queryString(values: Record<string, string | number | undefined | null>) {
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

export const api = {
  getSession: () => request<SessionResponse>('/auth/session'),
  localLogin: (username: string, password: string) =>
    request<SessionResponse>('/auth/local/login', {
      method: 'POST',
      body: JSON.stringify({ username, password }),
    }),
  logout: () => request<void>('/auth/logout', { method: 'POST' }),
  listUsers: (filters: { status?: string; auth_method?: string; query?: string; cursor?: string } = {}) =>
    request<CursorResponse<UserSummary>>(`/users${queryString(filters)}`),
  getUser: (id: string) => request<User>(`/users/${encodeURIComponent(id)}`),
  setUserStatus: (id: string, status: UserStatus) => request<User>(`/users/${encodeURIComponent(id)}`, json('PATCH', { status })),
  replaceUserRoles: (id: string, role_ids: string[]) => request<User>(`/users/${encodeURIComponent(id)}/roles`, json('PUT', { role_ids })),
  listRoles: () => request<{ items: Role[] }>('/roles'),
  createRole: (body: { key: string; display_name: string; description?: string }) => request<Role>('/roles', json('POST', body)),
  replaceSystemPermissions: (id: string, permissions: SystemPermission[]) => request<Role>(`/roles/${encodeURIComponent(id)}/system-permissions`, json('PUT', { permissions })),
  replaceModelPermissions: (id: string, grants: Array<{ model_id: string; permissions: ModelPermission[] }>) => request<Role>(`/roles/${encodeURIComponent(id)}/model-permissions`, json('PUT', { grants })),
  listModels: (status?: string) => request<{ items: ContentModelSummary[] }>(`/models${queryString({ status })}`),
  getModel: (id: string) => request<ContentModel>(`/models/${encodeURIComponent(id)}`),
  createModel: (body: { key: string; display_name: string; description?: string }) => request<ContentModel>('/models', json('POST', body)),
  createField: (modelId: string, body: ContentFieldInput) => request<ContentField>(`/models/${encodeURIComponent(modelId)}/fields`, json('POST', body)),
  listEntries: (modelId: string, status = 'draft', cursor?: string) => request<CursorResponse<ContentEntrySummary>>(`/models/${encodeURIComponent(modelId)}/entries${queryString({ status, cursor })}`),
  getEntry: (modelId: string, entryId: string) => request<ContentEntry>(`/models/${encodeURIComponent(modelId)}/entries/${encodeURIComponent(entryId)}`),
  createEntry: (modelId: string, content: Record<string, unknown>) => request<ContentEntry>(`/models/${encodeURIComponent(modelId)}/entries`, json('POST', { content })),
  updateEntry: (modelId: string, entryId: string, base_revision_id: string, content: Record<string, unknown>) => request<ContentEntry>(`/models/${encodeURIComponent(modelId)}/entries/${encodeURIComponent(entryId)}`, json('PATCH', { base_revision_id, content })),
  listAuditEvents: (filters: Record<string, string | undefined>) => request<CursorResponse<AuditEvent>>(`/audit/events${queryString(filters)}`),
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

export function oidcStartUrl(returnTo: string) {
  const safePath = safeReturnTo(returnTo)
  if (!safePath) {
    throw new TypeError('return_to 必须是同源绝对路径')
  }
  return `${API_BASE}/auth/oidc/start?${new URLSearchParams({ return_to: safePath })}`
}
