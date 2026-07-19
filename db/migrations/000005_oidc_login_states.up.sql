CREATE TABLE oidc_login_states (
    state_hash BINARY(32) NOT NULL,
    browser_binding_hash BINARY(32) NOT NULL,
    nonce VARCHAR(255) NOT NULL,
    pkce_verifier VARCHAR(255) NOT NULL,
    return_to VARCHAR(2048) NOT NULL,
    expires_at DATETIME(6) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (state_hash),
    KEY idx_oidc_login_states_expiry (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;
