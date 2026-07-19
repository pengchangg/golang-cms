CREATE TABLE role_model_permissions (
    role_id VARCHAR(64) NOT NULL,
    model_id VARCHAR(64) NOT NULL,
    permission VARCHAR(64) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (role_id, model_id, permission),
    KEY idx_role_model_permissions_model (model_id, role_id),
    CONSTRAINT fk_role_model_permissions_role FOREIGN KEY (role_id) REFERENCES roles (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;
