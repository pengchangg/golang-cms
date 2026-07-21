export const systemPermissionCodes = [
  'users.view',
  'users.manage',
  'roles.view',
  'roles.manage',
  'models.view',
  'models.create',
  'models.update',
  'models.archive',
  'assets.view',
  'assets.upload',
  'assets.update',
  'assets.archive',
  'api_keys.view',
  'api_keys.create',
  'api_keys.revoke',
  'audit.view',
] as const

export type SystemPermission = (typeof systemPermissionCodes)[number]

export const modelPermissionCodes = [
  'content.view', 'content.create', 'content.update', 'content.archive',
  'content.submit', 'content.review', 'content.publish', 'content.unpublish',
] as const

export type ModelPermission = (typeof modelPermissionCodes)[number]

export interface Principal {
  user_id: string
  display_name: string
  email: string | null
  auth_method: 'oidc' | 'local'
  system_permissions: SystemPermission[]
  model_permissions: Array<{
    model_id: string
    permissions: ModelPermission[]
  }>
}

export interface SessionResponse {
  principal: Principal
  content_models: Array<{ id: string; key: string; display_name: string }>
  csrf_token: string
  idle_expires_at: string
  expires_at: string
}

export interface ValidationDetail {
  path: string
  code: string
  message: string
}

export interface ErrorResponse {
  error: {
    code: string
    message: string
    request_id: string
    details: ValidationDetail[]
  }
}

export type ResourceStatus = 'active' | 'archived'
export type UserStatus = 'enabled' | 'disabled'
export type AuthMethod = 'oidc' | 'local'
export type EntryStatus = 'draft' | 'archived'
export type FieldType =
  | 'single_line_text' | 'multi_line_text' | 'rich_text' | 'integer' | 'decimal'
  | 'boolean' | 'date' | 'datetime' | 'single_select' | 'multi_select' | 'json'
  | 'single_media' | 'multi_media' | 'single_relation' | 'multi_relation'
  | 'object' | 'repeatable_group'

export interface CursorResponse<T> { items: T[]; next_cursor: string | null }
export interface UserSummary {
  id: string; display_name: string; email: string | null; auth_methods: AuthMethod[]
  is_emergency_admin: boolean; status: UserStatus; created_at: string; updated_at: string
}
export interface User extends UserSummary { role_ids: string[] }
export interface Role {
  id: string; key: string; display_name: string; description: string
  system_permissions: SystemPermission[]
  model_permissions: Array<{ model_id: string; permissions: ModelPermission[] }>
  created_at: string; updated_at: string
}
export interface FieldConstraints {
  min_length?: number; max_length?: number; minimum?: string; maximum?: string
  enum_options?: Array<{ value: string; label: string }>; target_model_id?: string
  unique?: boolean; filterable?: boolean; sortable?: boolean
}
export interface ContentFieldInput {
  key: string; display_name: string; description?: string; type: FieldType; required?: boolean
  default_value?: unknown; constraints?: FieldConstraints; children?: ContentFieldInput[]
}
export interface ContentFieldPatch {
  display_name?: string; description?: string; type?: FieldType; required?: boolean
  default_value?: unknown; constraints?: FieldConstraints; children?: ContentFieldInput[]
}
export interface UpdateFieldOrderRequest {
  parent_id: string | null
  base_field_ids: string[]
  field_ids: string[]
}
export interface ContentField extends Omit<ContentFieldInput, 'description' | 'required' | 'constraints' | 'children'> {
  id: string; description: string; required: boolean; default_value: unknown
  constraints: FieldConstraints; children: ContentField[]; status: ResourceStatus
  created_at: string; updated_at: string
}
export interface ContentModelSummary {
  id: string; key: string; display_name: string; description: string; status: ResourceStatus
  created_at: string; updated_at: string
}
export interface ContentModel extends ContentModelSummary { fields: ContentField[] }
export interface ContentRevision {
  id: string; entry_id: string; model_id: string; number: number
  content: Record<string, unknown>; created_by: string; created_at: string
  workflow_status: WorkflowStatus; submitted_by: string | null; submitted_at: string | null
}
export type WorkflowStatus = 'draft' | 'pending_review' | 'rejected' | 'published' | 'unpublished'
export type WorkflowRevision = ContentRevision
export interface ContentEntrySummary {
  id: string; model_id: string; status: EntryStatus; current_draft_revision_id: string
  current_draft_content: Record<string, unknown>
  workflow_status: WorkflowStatus; current_published_revision_id: string | null
  expanded?: Record<string, ContentEntrySummary | ContentEntrySummary[]>
  created_by: string; created_at: string; updated_at: string
}
export interface ContentEntry extends ContentEntrySummary {
  current_draft_revision: WorkflowRevision
  current_published_revision: WorkflowRevision | null
}
export interface WorkflowEvent {
  id: string; entry_id: string; revision_id: string
  type: 'submitted' | 'approved' | 'rejected' | 'unpublished'
  from_status: WorkflowStatus; to_status: WorkflowStatus
  actor_id: string; reason: string | null; occurred_at: string
}
export interface EntryListQuery {
  status?: EntryStatus; workflow_status?: WorkflowStatus; cursor?: string; limit?: number
  filter?: string; relation_filter?: string; sort?: string; expand?: string; include_total?: boolean
}
export interface EntryListResponse extends CursorResponse<ContentEntrySummary> {
  fields: Array<{
    key: string; display_name: string; type: FieldType
    constraints: Pick<FieldConstraints, 'enum_options' | 'filterable' | 'sortable'>
  }>
  total?: number; total_is_estimate?: boolean
}
export type APIKeyStatus = 'active' | 'expired' | 'revoked'
export interface APIKey {
  id: string; name: string; prefix: string; model_ids: string[]; status: APIKeyStatus
  expires_at: string | null; revoked_at: string | null; last_used_at: string | null
  rotated_from_id: string | null; replaced_by_id: string | null
  created_by: string; created_at: string
}
export interface APIKeySecret extends APIKey { key: string }
export interface CreateAPIKeyRequest { name: string; model_ids: string[]; expires_at: string | null }
export interface RotateAPIKeyRequest { name?: string; model_ids?: string[]; expires_at?: string | null }
export interface AuditEvent {
  id: string; occurred_at: string; request_id: string; actor_type: 'user' | 'system'
  actor_id: string | null; actor_display_name: string | null; action: string; resource_type: string; resource_id: string | null
  result: 'success' | 'failure'; ip: string; user_agent: string
  changes: Record<string, unknown>; failure_code: string | null
}

export type AssetStatus = 'quarantined' | 'available' | 'archived'
export interface Asset {
  id: string; filename: string; mime_type: string; size: number; sha256: string; etag: string | null
  status: AssetStatus; created_by: string; created_at: string; confirmed_at: string | null; archived_at: string | null
}
export interface SignedUpload { method: 'PUT'; url: string; headers: Record<string, string>; expires_at: string }
export interface AssetUpload { asset: Asset; upload: SignedUpload }
export interface CreateAssetUploadRequest { filename: string; mime_type: string; size: number; sha256: string }

export interface ExportCSVQuery {
  workflow_status?: WorkflowStatus; filter?: string; relation_filter?: string; sort?: string
}
