CREATE TABLE content_draft_pointers (
    entry_id VARCHAR(36) NOT NULL,
    model_id VARCHAR(36) NOT NULL,
    revision_id VARCHAR(36) NOT NULL,
    PRIMARY KEY (entry_id),
    UNIQUE KEY uq_content_draft_pointers_entry_model (entry_id, model_id),
    CONSTRAINT fk_content_draft_pointers_entry FOREIGN KEY (entry_id, model_id) REFERENCES content_entries (id, model_id),
    CONSTRAINT fk_content_draft_pointers_revision FOREIGN KEY (revision_id, entry_id, model_id) REFERENCES content_revisions (id, entry_id, model_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;
