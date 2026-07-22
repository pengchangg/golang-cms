const mockSession = {
  principal: {
    user_id: 'usr_dev_preview',
    display_name: '开发预览用户',
    email: 'preview@local.invalid',
    auth_method: 'sms',
    is_emergency_admin: false,
    has_high_risk_role: false,
    system_permissions: ['users.view', 'users.manage', 'roles.view', 'roles.manage', 'models.view', 'models.create', 'models.update', 'api_keys.view', 'api_keys.create', 'api_keys.revoke', 'audit.view'],
    model_permissions: [{ model_id: 'mdl_articles', permissions: ['content.view', 'content.create', 'content.update', 'content.submit', 'content.review', 'content.publish', 'content.unpublish'] }],
  },
  content_models: [{ id: 'mdl_articles', key: 'articles', display_name: '内部文章' }],
  csrf_token: 'dev-only-csrf-token-0000000000000000',
  idle_expires_at: '2099-01-01T00:00:00Z',
  expires_at: '2099-01-01T12:00:00Z',
}

export function enableAuthMock() {
  const nativeFetch = window.fetch.bind(window)
  window.fetch = async (input, init) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
    if (!url.startsWith('/api/admin/v1/auth/')) return nativeFetch(input, init)
    if (url.includes('/captcha/challenges')) return Response.json({ challenge_id: 'cap_dev', background_image: 'data:image/svg+xml,%3Csvg xmlns="http://www.w3.org/2000/svg" width="300" height="220"%3E%3Crect width="300" height="220" fill="%23dce7df"/%3E%3Cpath d="M0 180L80 75l45 34 56-62 119 107v66H0z" fill="%2381a18e"/%3E%3C/svg%3E', tile_image: 'data:image/svg+xml,%3Csvg xmlns="http://www.w3.org/2000/svg" width="64" height="64"%3E%3Crect width="64" height="64" rx="8" fill="%23f4f3ee" stroke="%23256b55" stroke-width="4"/%3E%3C/svg%3E', tile_x: 120, tile_y: 78, expires_at: '2099-01-01T00:02:00Z' })
    if (url.endsWith('/auth/sms/challenges')) return Response.json({ challenge_id: 'sms_dev', phone_masked: '138****8000', expires_at: '2099-01-01T00:05:00Z', retry_after_seconds: 60 })
    if (url.includes('/auth/sms/challenges/') && url.endsWith('/verify')) return Response.json(mockSession)
    if (url.includes('/logout')) return new Response(null, { status: 204 })
    return Response.json(mockSession)
  }
}
