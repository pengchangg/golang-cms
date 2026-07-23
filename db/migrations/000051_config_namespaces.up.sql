CREATE TABLE config_namespaces (
    id VARCHAR(36) NOT NULL,
    namespace_key VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    display_name VARCHAR(120) NOT NULL,
    description VARCHAR(1000) NOT NULL DEFAULT '',
    status ENUM('active', 'archived') NOT NULL DEFAULT 'active',
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_config_namespaces_key (namespace_key),
    KEY idx_config_namespaces_status_key (status, namespace_key),
    CONSTRAINT chk_config_namespaces_key CHECK (namespace_key REGEXP _ascii'^[a-z][a-z0-9_]{0,63}$')
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;
