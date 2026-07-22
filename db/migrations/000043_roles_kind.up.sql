ALTER TABLE roles ADD COLUMN kind ENUM('custom', 'high_risk') NOT NULL DEFAULT 'custom' AFTER `key`;
