-- Layer 1: ngữ cảnh request & actor
CREATE TABLE IF NOT EXISTS audits (
    id VARCHAR(64) PRIMARY KEY,
    tenant_id VARCHAR(64) NOT NULL,
    request_id VARCHAR(128) NOT NULL DEFAULT '',
    actor_id VARCHAR(64) NOT NULL,
    actor_username VARCHAR(128) NOT NULL DEFAULT '',
    actor_role VARCHAR(64) NOT NULL DEFAULT '',
    actor_security_level VARCHAR(64) NOT NULL DEFAULT '',
    client_ip VARCHAR(64) NOT NULL DEFAULT '',
    user_agent TEXT NOT NULL DEFAULT '',
    client_info JSONB NOT NULL DEFAULT '{}'::jsonb,
    source_module VARCHAR(64) NOT NULL,
    action_method VARCHAR(16) NOT NULL,
    action_path VARCHAR(255) NOT NULL,
    action_name VARCHAR(128) NOT NULL DEFAULT '',
    status_code INT NOT NULL,
    security_events JSONB NOT NULL DEFAULT '[]'::jsonb,
    occurred_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_audits_occurred_at ON audits (occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_audits_actor ON audits (actor_id);

-- Layer 2: bảng/bản ghi bị tác động kèm snapshot trước/sau
CREATE TABLE IF NOT EXISTS entity_changes (
    id VARCHAR(64) PRIMARY KEY,
    audit_id VARCHAR(64) NOT NULL REFERENCES audits(id),
    tenant_id VARCHAR(64) NOT NULL,
    table_name VARCHAR(128) NOT NULL,
    entity_name VARCHAR(128) NOT NULL,
    entity_id VARCHAR(64) NOT NULL,
    operation VARCHAR(16) NOT NULL CHECK (operation IN ('INSERT', 'UPDATE', 'DELETE')),
    before_snapshot JSONB,
    after_snapshot JSONB,
    changed_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_entity_changes_audit ON entity_changes (audit_id);
CREATE INDEX IF NOT EXISTS idx_entity_changes_entity ON entity_changes (table_name, entity_id);

-- Layer 3: diff từng trường + hash chain SHA-256.
-- seq xác định thứ tự chuỗi; entity_change_id/field_name NULL là "seal" của audit không có diff (VD: LOGIN_FAILED)
CREATE TABLE IF NOT EXISTS entity_audit_logs (
    seq BIGSERIAL UNIQUE,
    id VARCHAR(64) PRIMARY KEY,
    entity_change_id VARCHAR(64) REFERENCES entity_changes(id),
    audit_id VARCHAR(64) NOT NULL REFERENCES audits(id),
    tenant_id VARCHAR(64) NOT NULL,
    field_name VARCHAR(128),
    old_value TEXT,
    new_value TEXT,
    prev_hash VARCHAR(64) NOT NULL,
    integrity_hash VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_entity_audit_logs_audit ON entity_audit_logs (audit_id);
