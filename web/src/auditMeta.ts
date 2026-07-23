import type { AuditEvent, Principal } from './api/types'
import { hasModelPermission, hasSystemPermission } from './auth/permissions'
import { ASSETS_ENABLED } from './config'

export const auditActionLabels: Record<string, string> = {
  auth_sms_challenge_created: '发送短信验证码',
  auth_sms_login_succeeded: '手机号登录成功',
  auth_sms_login_failed: '手机号登录失败',
  auth_local_login_succeeded: '本地应急登录成功',
  auth_local_login_failed: '本地应急登录失败',
  auth_logout_succeeded: '退出登录',
  auth_local_password_reset: '重置应急管理员密码',
  user_status_updated: '更新用户状态',
  user_created: '创建手机号账户',
  user_phone_updated: '更新用户手机号',
  user_roles_replaced: '调整用户角色',
  role_created: '创建角色',
  role_updated: '更新角色',
  role_deleted: '删除角色',
  role_system_permissions_replaced: '调整系统权限',
  role_model_permissions_replaced: '调整模型权限',
  configuration_namespace_created: '创建配置命名空间',
  configuration_namespace_updated: '更新配置命名空间',
  configuration_namespace_archived: '归档配置命名空间',
  configuration_item_created: '创建配置项',
  configuration_item_updated: '更新配置项',
  configuration_item_archived: '归档配置项',
  configuration_revision_created: '保存配置版本',
  configuration_revision_submitted: '提交配置审核',
  configuration_revision_approved: '审核通过并发布配置',
  configuration_revision_rejected: '驳回配置版本',
  configuration_revision_unpublished: '下线配置版本',
  model_created: '创建内容模型',
  model_updated: '更新内容模型',
  model_archived: '归档内容模型',
  model_field_created: '创建模型字段',
  model_field_updated: '更新模型字段',
  model_field_archived: '归档模型字段',
  content_entry_created: '创建内容草稿',
  content_revision_created: '保存新内容版本',
  content_entry_archived: '归档内容',
  content_revision_submitted: '提交内容审核',
  content_revision_approved: '审核通过并发布',
  content_revision_rejected: '驳回内容版本',
  content_revision_unpublished: '下线内容版本',
  api_key_created: '创建 API Key',
  api_key_revoked: '撤销 API Key',
  api_key_rotated: '轮换 API Key',
  asset_upload_created: '申请素材上传',
  asset_upload_confirmed: '确认素材上传',
  asset_downloaded: '下载素材',
  asset_archived: '归档素材',
}

export const auditResourceLabels: Record<string, string> = {
  authentication: '认证活动', user: '用户', role: '角色', content_model: '内容模型',
  content_field: '模型字段', content_entry: '内容条目', content_revision: '内容版本',
  api_key: 'API Key', asset: '素材', configuration_namespace: '配置命名空间', configuration_item: '配置项', configuration_revision: '配置版本',
}

function changeID(event: AuditEvent, key: string) {
  const value = event.changes[key]
  return typeof value === 'string' ? value : undefined
}

export function auditResourcePath(event: AuditEvent, principal: Principal) {
  const id = event.resource_id
  const modelID = changeID(event, 'model_id')
  const entryID = changeID(event, 'entry_id')
  if (event.resource_type === 'content_model' && id && hasSystemPermission(principal, 'models.view')) return `/models/${encodeURIComponent(id)}`
  if (event.resource_type === 'content_field' && modelID && hasSystemPermission(principal, 'models.view')) return `/models/${encodeURIComponent(modelID)}`
  if (event.resource_type === 'content_entry' && id && modelID && hasModelPermission(principal, modelID, 'content.view')) return `/content/${encodeURIComponent(modelID)}/${encodeURIComponent(id)}`
  if (event.resource_type === 'content_revision' && modelID && entryID && hasModelPermission(principal, modelID, 'content.view')) return `/content/${encodeURIComponent(modelID)}/${encodeURIComponent(entryID)}`
  if (event.resource_type === 'user' && hasSystemPermission(principal, 'users.view')) return '/users'
  if (event.resource_type === 'role' && hasSystemPermission(principal, 'roles.view')) return '/roles'
  if (event.resource_type === 'api_key' && hasSystemPermission(principal, 'api_keys.view')) return '/api-keys'
  if (event.resource_type === 'asset' && ASSETS_ENABLED && hasSystemPermission(principal, 'assets.view')) return '/assets'
  if (event.resource_type === 'configuration_namespace' && id && hasSystemPermission(principal, 'configurations.view')) return `/configurations/${encodeURIComponent(id)}`
  if ((event.resource_type === 'configuration_item' || event.resource_type === 'configuration_revision') && hasSystemPermission(principal, 'configurations.view')) {
    const namespaceID = changeID(event, 'namespace_id')
    const itemID = event.resource_type === 'configuration_item' ? id : changeID(event, 'item_id')
    if (namespaceID && itemID) return `/configurations/${encodeURIComponent(namespaceID)}/items/${encodeURIComponent(itemID)}`
  }
  if (event.action === 'auth_local_password_reset' && hasSystemPermission(principal, 'users.view')) return '/users'
  return undefined
}
