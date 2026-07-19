const mockSession = {
  principal: {
    user_id: 'usr_dev_preview',
    display_name: '开发预览用户',
    email: 'preview@local.invalid',
    auth_method: 'oidc',
    system_permissions: ['users.view', 'users.manage', 'roles.view', 'roles.manage', 'models.view', 'models.create', 'models.update', 'audit.view'],
    model_permissions: [{ model_id: 'mdl_articles', permissions: ['content.view', 'content.create', 'content.update'] }],
  },
  csrf_token: 'dev-only-csrf-token-0000000000000000',
  idle_expires_at: '2099-01-01T00:00:00Z',
  expires_at: '2099-01-01T12:00:00Z',
}

export function enableAuthMock() {
  const nativeFetch = window.fetch.bind(window)
  window.fetch = async (input, init) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
    if (!url.startsWith('/api/admin/v1/auth/')) return nativeFetch(input, init)
    if (url.includes('/logout')) return new Response(null, { status: 204 })
    return Response.json(mockSession)
  }
}
