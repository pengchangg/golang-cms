CREATE TABLE local_credentials (
    user_id VARCHAR(64) NOT NULL,
    username VARCHAR(128) NOT NULL,
    password_hash VARCHAR(512) NOT NULL,
    emergency_admin BOOLEAN NOT NULL DEFAULT TRUE,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (user_id),
    UNIQUE KEY uq_local_credentials_username (username),
    CONSTRAINT fk_local_credentials_user FOREIGN KEY (user_id) REFERENCES users (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;
