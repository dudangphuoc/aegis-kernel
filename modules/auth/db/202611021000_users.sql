-- Tài khoản người dùng của tenant (tại store). Mật khẩu và mã MFA được che khi ghi audit (AuditTables của module).
CREATE TABLE IF NOT EXISTS users (
    id            text        NOT NULL,
    store_id      uuid        NOT NULL DEFAULT platform.store_id(),
    tenant_id     text        NOT NULL,
    username      text        NOT NULL,
    password      text        NOT NULL,
    role          text        NOT NULL DEFAULT 'TENANT_ADMIN',
    mfa_code      text        NOT NULL DEFAULT '654321',
    security_tier text        NOT NULL DEFAULT 'STANDARD',
    created_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (store_id, id),
    UNIQUE (store_id, username)
);
