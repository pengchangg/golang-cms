CREATE TABLE content_revisions (
    id VARCHAR(36) NOT NULL,
    entry_id VARCHAR(36) NOT NULL,
    model_id VARCHAR(36) NOT NULL,
    revision_number INT UNSIGNED NOT NULL,
    content JSON NOT NULL,
    created_by VARCHAR(36) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_content_revisions_entry_number (entry_id, revision_number),
    UNIQUE KEY uq_content_revisions_id_entry_model (id, entry_id, model_id),
    KEY idx_content_revisions_entry_page (entry_id, revision_number DESC),
    CONSTRAINT fk_content_revisions_entry FOREIGN KEY (entry_id, model_id) REFERENCES content_entries (id, model_id),
    CONSTRAINT fk_content_revisions_creator FOREIGN KEY (created_by) REFERENCES users (id),
    CONSTRAINT chk_content_revisions_number CHECK (revision_number >= 1)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;
