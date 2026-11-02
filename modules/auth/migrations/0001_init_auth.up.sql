CREATE TABLE IF NOT EXISTS users (
    id VARCHAR(64) PRIMARY KEY,
    tenant_id VARCHAR(64) NOT NULL,
    username VARCHAR(128) NOT NULL UNIQUE,
    password VARCHAR(255) NOT NULL,
    role VARCHAR(64) NOT NULL DEFAULT 'TENANT_ADMIN',
    mfa_code VARCHAR(32) NOT NULL DEFAULT '654321',
    security_tier VARCHAR(64) NOT NULL DEFAULT 'STANDARD',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Nhật ký bảo mật nằm ở module audit (audit_<tenant_id>), không lưu ở đây
INSERT INTO users (id, tenant_id, username, password, role, mfa_code)
VALUES ('USR-ADMIN-01', current_database(), 'admin', 'pass123', 'TENANT_ADMIN', '654321')
ON CONFLICT (username) DO NOTHING;
