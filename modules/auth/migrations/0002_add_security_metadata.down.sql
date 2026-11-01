ALTER TABLE security_audit_logs DROP COLUMN IF EXISTS client_ip;
ALTER TABLE users DROP COLUMN IF EXISTS security_tier;

