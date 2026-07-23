CREATE TABLE config_published_pointers (
    item_id VARCHAR(36) NOT NULL,
    namespace_id VARCHAR(36) NOT NULL,
    revision_id VARCHAR(36) NOT NULL,
    published_at DATETIME(6) NOT NULL,
    PRIMARY KEY (item_id),
    UNIQUE KEY uq_config_published_pointers_item_namespace (item_id, namespace_id),
    KEY idx_config_published_pointers_namespace_page (namespace_id, published_at DESC, item_id DESC, revision_id),
    CONSTRAINT fk_config_published_pointers_item FOREIGN KEY (item_id, namespace_id) REFERENCES config_items (id, namespace_id),
    CONSTRAINT fk_config_published_pointers_revision FOREIGN KEY (revision_id, item_id, namespace_id) REFERENCES config_revisions (id, item_id, namespace_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;
