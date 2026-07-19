CREATE TABLE audit_events (
    id VARCHAR(64) NOT NULL,
    occurred_at DATETIME(6) NOT NULL,
    request_id VARCHAR(128) NOT NULL,
    actor_type ENUM('user', 'system') NOT NULL,
    actor_id VARCHAR(64) NULL,
    action VARCHAR(128) NOT NULL,
    resource_type VARCHAR(128) NOT NULL,
    resource_id VARCHAR(64) NULL,
    result ENUM('success', 'failure') NOT NULL,
    ip VARCHAR(45) NOT NULL,
    user_agent VARCHAR(512) NOT NULL,
    changes JSON NOT NULL,
    failure_code VARCHAR(128) NULL,
    PRIMARY KEY (id),
    KEY idx_audit_events_occurred (occurred_at, id),
    KEY idx_audit_events_actor (actor_type, actor_id, occurred_at),
    CONSTRAINT chk_audit_events_failure CHECK ((result = 'success' AND failure_code IS NULL) OR (result = 'failure' AND failure_code IS NOT NULL))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;
