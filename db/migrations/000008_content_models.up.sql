CREATE TABLE content_models (
    id VARCHAR(36) NOT NULL,
    model_key VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    display_name VARCHAR(120) NOT NULL,
    description VARCHAR(1000) NOT NULL DEFAULT '',
    status ENUM('active', 'archived') NOT NULL DEFAULT 'active',
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_content_models_key (model_key),
    KEY idx_content_models_status_key (status, model_key),
    CONSTRAINT chk_content_models_key CHECK (model_key REGEXP _ascii'^[a-z][a-z0-9_]{0,63}$')
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;
