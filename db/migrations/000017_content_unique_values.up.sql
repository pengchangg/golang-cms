CREATE TABLE content_unique_values (
    model_id VARCHAR(36) NOT NULL,
    field_id VARCHAR(36) NOT NULL,
    canonical_value BINARY(32) NOT NULL,
    entry_id VARCHAR(36) NOT NULL,
    PRIMARY KEY (model_id, field_id, canonical_value),
    UNIQUE KEY uq_content_unique_values_entry_field (entry_id, field_id),
    CONSTRAINT fk_content_unique_values_field FOREIGN KEY (field_id, model_id) REFERENCES content_fields (id, model_id),
    CONSTRAINT fk_content_unique_values_entry FOREIGN KEY (entry_id, model_id) REFERENCES content_entries (id, model_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;
