CREATE TABLE content_entries (
    id VARCHAR(36) NOT NULL,
    model_id VARCHAR(36) NOT NULL,
    status ENUM('draft', 'archived') NOT NULL DEFAULT 'draft',
    created_by VARCHAR(36) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_content_entries_id_model (id, model_id),
    KEY idx_content_entries_model_status_page (model_id, status, updated_at DESC, id DESC),
    CONSTRAINT fk_content_entries_model FOREIGN KEY (model_id) REFERENCES content_models (id),
    CONSTRAINT fk_content_entries_creator FOREIGN KEY (created_by) REFERENCES users (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;
