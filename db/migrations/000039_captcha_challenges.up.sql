CREATE TABLE captcha_challenges (
    id_hash BINARY(32) NOT NULL,
    browser_binding_hash BINARY(32) NOT NULL,
    target_x SMALLINT UNSIGNED NOT NULL,
    target_y SMALLINT UNSIGNED NOT NULL,
    attempts_remaining TINYINT UNSIGNED NOT NULL,
    expires_at DATETIME(6) NOT NULL,
    consumed_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id_hash),
    KEY idx_captcha_challenges_expiry (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;
