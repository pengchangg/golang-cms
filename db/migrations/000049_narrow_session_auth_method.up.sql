ALTER TABLE sessions MODIFY COLUMN auth_method ENUM('local', 'sms') NOT NULL;
