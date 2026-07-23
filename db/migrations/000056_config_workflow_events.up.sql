CREATE TABLE config_workflow_events (
    id VARCHAR(36) NOT NULL,
    item_id VARCHAR(36) NOT NULL,
    namespace_id VARCHAR(36) NOT NULL,
    revision_id VARCHAR(36) NOT NULL,
    event_type ENUM('submitted', 'approved', 'rejected', 'unpublished') NOT NULL,
    from_status ENUM('draft', 'pending_review', 'rejected', 'published', 'unpublished') NOT NULL,
    to_status ENUM('draft', 'pending_review', 'rejected', 'published', 'unpublished') NOT NULL,
    actor_id VARCHAR(64) NOT NULL,
    reason VARCHAR(1000) NULL,
    occurred_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    KEY idx_config_workflow_events_item_page (item_id, occurred_at DESC, id DESC),
    CONSTRAINT fk_config_workflow_events_item FOREIGN KEY (item_id, namespace_id) REFERENCES config_items (id, namespace_id),
    CONSTRAINT fk_config_workflow_events_revision FOREIGN KEY (revision_id, item_id, namespace_id) REFERENCES config_revisions (id, item_id, namespace_id),
    CONSTRAINT fk_config_workflow_events_actor FOREIGN KEY (actor_id) REFERENCES users (id),
    CONSTRAINT chk_config_workflow_events_reason CHECK ((event_type = 'rejected' AND reason IS NOT NULL AND CHAR_LENGTH(reason) BETWEEN 1 AND 1000) OR (event_type <> 'rejected' AND reason IS NULL))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;
