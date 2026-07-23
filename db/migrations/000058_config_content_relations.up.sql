CREATE TABLE config_content_relations (
    revision_id VARCHAR(36) NOT NULL,
    item_id VARCHAR(36) NOT NULL,
    namespace_id VARCHAR(36) NOT NULL,
    target_entry_id VARCHAR(36) NOT NULL,
    target_model_id VARCHAR(36) NOT NULL,
    position TINYINT UNSIGNED NOT NULL,
    PRIMARY KEY (revision_id, position),
    UNIQUE KEY uq_config_content_relations_target (revision_id, target_entry_id),
    KEY idx_config_content_relations_source (namespace_id, item_id, revision_id),
    KEY idx_config_content_relations_target_entry (target_model_id, target_entry_id),
    CONSTRAINT fk_config_content_relations_revision FOREIGN KEY (revision_id, item_id, namespace_id) REFERENCES config_revisions (id, item_id, namespace_id),
    CONSTRAINT fk_config_content_relations_target FOREIGN KEY (target_entry_id, target_model_id) REFERENCES content_entries (id, model_id),
    CONSTRAINT chk_config_content_relations_position CHECK (position < 50)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;
