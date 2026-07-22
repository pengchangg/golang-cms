UPDATE roles SET `key`=CONCAT('legacy_high_risk_admin_', LOWER(REPLACE(UUID(), '-', ''))) WHERE `key`='high_risk_admin';
