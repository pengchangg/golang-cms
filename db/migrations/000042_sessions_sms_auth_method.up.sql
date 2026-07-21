ALTER TABLE sessions MODIFY COLUMN auth_method ENUM('oidc', 'local', 'sms') NOT NULL;
