UPDATE sessions SET revoked_at=COALESCE(revoked_at, CURRENT_TIMESTAMP(6)) WHERE auth_method='oidc';
