CREATE TABLE api_key_config_namespace_scopes (
    api_key_id VARCHAR(36) NOT NULL,
    namespace_id VARCHAR(36) NOT NULL,
    PRIMARY KEY (api_key_id, namespace_id),
    KEY idx_api_key_config_namespace_scopes_namespace (namespace_id, api_key_id),
    CONSTRAINT fk_api_key_config_namespace_scopes_key FOREIGN KEY (api_key_id) REFERENCES api_keys (id),
    CONSTRAINT fk_api_key_config_namespace_scopes_namespace FOREIGN KEY (namespace_id) REFERENCES config_namespaces (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;
