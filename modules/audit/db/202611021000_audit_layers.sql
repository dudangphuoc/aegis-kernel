-- Sổ kiểm toán 3 tầng. Là bảng cục bộ (localTables): không capture (tránh audit tự sinh audit), không đồng bộ.

-- Layer 1: ngữ cảnh request & actor
CREATE TABLE IF NOT EXISTS audits (
    id                   text        PRIMARY KEY,
    tenant_id            text        NOT NULL,
    request_id           text        NOT NULL DEFAULT '',
    actor_id             text        NOT NULL,
    actor_username       text        NOT NULL DEFAULT '',
    actor_role           text        NOT NULL DEFAULT '',
    actor_security_level text        NOT NULL DEFAULT '',
    client_ip            text        NOT NULL DEFAULT '',
    user_agent           text        NOT NULL DEFAULT '',
    client_info          jsonb       NOT NULL DEFAULT '{}'::jsonb,
    source_module        text        NOT NULL,
    action_method        text        NOT NULL,
    action_path          text        NOT NULL,
    action_name          text        NOT NULL DEFAULT '',
    status_code          int         NOT NULL,
    security_events      jsonb       NOT NULL DEFAULT '[]'::jsonb,
    occurred_at          timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_audits_occurred_at ON audits (occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_audits_actor ON audits (actor_id);

-- Layer 2: bảng/bản ghi bị tác động kèm snapshot trước/sau
CREATE TABLE IF NOT EXISTS entity_changes (
    id              text        PRIMARY KEY,
    audit_id        text        NOT NULL REFERENCES audits (id),
    tenant_id       text        NOT NULL,
    table_name      text        NOT NULL,
    entity_name     text        NOT NULL,
    entity_id       text        NOT NULL,
    operation       text        NOT NULL CHECK (operation IN ('INSERT', 'UPDATE', 'DELETE')),
    before_snapshot jsonb,
    after_snapshot  jsonb,
    changed_at      timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_entity_changes_audit ON entity_changes (audit_id);
CREATE INDEX IF NOT EXISTS idx_entity_changes_entity ON entity_changes (table_name, entity_id);

-- Layer 3: diff từng trường + hash chain SHA-256.
-- seq xác định thứ tự chuỗi; entity_change_id/field_name NULL là "seal" của audit không có diff (VD: LOGIN_FAILED)
CREATE TABLE IF NOT EXISTS entity_audit_logs (
    seq              bigserial   UNIQUE,
    id               text        PRIMARY KEY,
    entity_change_id text        REFERENCES entity_changes (id),
    audit_id         text        NOT NULL REFERENCES audits (id),
    tenant_id        text        NOT NULL,
    field_name       text,
    old_value        text,
    new_value        text,
    prev_hash        text        NOT NULL,
    integrity_hash   text        NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_entity_audit_logs_audit ON entity_audit_logs (audit_id);
