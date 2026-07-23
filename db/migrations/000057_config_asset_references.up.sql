CREATE TABLE config_asset_references (
    revision_id VARCHAR(36) NOT NULL,
    item_id VARCHAR(36) NOT NULL,
    namespace_id VARCHAR(36) NOT NULL,
    asset_id VARCHAR(36) NOT NULL,
    position TINYINT UNSIGNED NOT NULL,
    PRIMARY KEY (revision_id, position),
    UNIQUE KEY uq_config_asset_references_value (revision_id, asset_id),
    KEY idx_config_asset_references_asset (asset_id, revision_id),
    KEY idx_config_asset_references_source (namespace_id, item_id, revision_id),
    CONSTRAINT fk_config_asset_references_revision FOREIGN KEY (revision_id, item_id, namespace_id) REFERENCES config_revisions (id, item_id, namespace_id),
    CONSTRAINT fk_config_asset_references_asset FOREIGN KEY (asset_id) REFERENCES assets (id),
    CONSTRAINT chk_config_asset_references_position CHECK (position < 50)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;
