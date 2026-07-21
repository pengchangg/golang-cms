CREATE TABLE sms_credentials (
    user_id VARCHAR(64) NOT NULL,
    phone_e164 VARCHAR(16) NOT NULL,
    phone_masked VARCHAR(24) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (user_id),
    UNIQUE KEY uq_sms_credentials_phone (phone_e164),
    CONSTRAINT fk_sms_credentials_user FOREIGN KEY (user_id) REFERENCES users (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;
