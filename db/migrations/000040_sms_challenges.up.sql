CREATE TABLE sms_challenges (
    id_hash BINARY(32) NOT NULL,
    browser_binding_hash BINARY(32) NOT NULL,
    phone_e164 VARCHAR(16) NOT NULL,
    phone_masked VARCHAR(24) NOT NULL,
    otp_hash BINARY(32) NOT NULL,
    attempts_remaining TINYINT UNSIGNED NOT NULL,
    expires_at DATETIME(6) NOT NULL,
    consumed_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id_hash),
    KEY idx_sms_challenges_phone_created (phone_e164, created_at),
    KEY idx_sms_challenges_expiry (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;
