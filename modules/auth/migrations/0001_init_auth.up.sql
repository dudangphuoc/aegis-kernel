CREATE TABLE IF NOT EXISTS users (
    id VARCHAR(64) PRIMARY KEY,
    tenant_id VARCHAR(64) NOT NULL,
    username VARCHAR(128) NOT NULL UNIQUE,
    password VARCHAR(255) NOT NULL,
    role VARCHAR(64) NOT NULL DEFAULT 'TENANT_ADMIN',
    mfa_code VARCHAR(32) NOT NULL DEFAULT '654321',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS security_audit_logs (
    id VARCHAR(64) PRIMARY KEY,
    tenant_id VARCHAR(64) NOT NULL,
    username VARCHAR(128) NOT NULL,
    action VARCHAR(128) NOT NULL,
    details TEXT NOT NULL,
    timestamp TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO users (id, tenant_id, username, password, role, mfa_code)
VALUES ('USR-ADMIN-01', current_database(), 'admin', 'pass123', 'TENANT_ADMIN', '654321')
ON CONFLICT (username) DO NOTHING;

