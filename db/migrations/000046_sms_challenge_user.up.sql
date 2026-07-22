ALTER TABLE sms_challenges ADD COLUMN user_id VARCHAR(64) NULL AFTER phone_masked, ADD CONSTRAINT fk_sms_challenges_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET NULL;
