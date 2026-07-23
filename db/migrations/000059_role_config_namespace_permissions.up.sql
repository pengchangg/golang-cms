CREATE TABLE role_config_namespace_permissions (
    role_id VARCHAR(64) NOT NULL,
    namespace_id VARCHAR(36) NOT NULL,
    permission VARCHAR(64) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (role_id, namespace_id, permission),
    KEY idx_role_config_namespace_permissions_namespace (namespace_id, role_id),
    CONSTRAINT fk_role_config_namespace_permissions_role FOREIGN KEY (role_id) REFERENCES roles (id) ON DELETE CASCADE,
    CONSTRAINT fk_role_config_namespace_permissions_namespace FOREIGN KEY (namespace_id) REFERENCES config_namespaces (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;
