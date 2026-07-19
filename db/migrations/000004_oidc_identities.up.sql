CREATE TABLE oidc_identities (
    issuer VARCHAR(512) NOT NULL,
    subject VARCHAR(255) NOT NULL,
    user_id VARCHAR(64) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (issuer, subject),
    KEY idx_oidc_identities_user (user_id),
    CONSTRAINT fk_oidc_identities_user FOREIGN KEY (user_id) REFERENCES users (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;
