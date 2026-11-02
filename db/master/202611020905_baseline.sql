-- DB control plane (schema public): operator, tenancy, hub, release, cờ feature, audit.

CREATE TABLE IF NOT EXISTS operator (
    id            uuid        PRIMARY KEY,
    email         text        NOT NULL UNIQUE,
    password_hash text        NOT NULL,
    level         text        NOT NULL CHECK (level IN ('support', 'admin', 'root')),
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS operator_session (
    id           uuid        PRIMARY KEY,
    operator_id  uuid        NOT NULL REFERENCES operator (id),
    refresh_hash text        NOT NULL UNIQUE,
    expires_at   timestamptz NOT NULL,
    revoked_at   timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS database_server (
    id                  uuid        PRIMARY KEY,
    name                text        NOT NULL UNIQUE,
    host                text        NOT NULL,
    port                int         NOT NULL,
    read_host           text,
    read_port           int,
    username            text        NOT NULL,
    password_ciphertext bytea       NOT NULL,
    password_nonce      bytea       NOT NULL,
    accepts_new_tenants boolean     NOT NULL DEFAULT true,
    created_at          timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS tenant (
    id              uuid        PRIMARY KEY,
    code            text        NOT NULL UNIQUE,
    name            text        NOT NULL,
    server_id       uuid        NOT NULL REFERENCES database_server (id),
    status          text        NOT NULL CHECK (status IN ('provisioning', 'ready', 'suspended', 'failed')),
    platform_status text        NOT NULL DEFAULT '',
    platform_error  text        NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS store (
    id                uuid        PRIMARY KEY,
    tenant_id         uuid        NOT NULL REFERENCES tenant (id),
    code              text        NOT NULL,
    name              text        NOT NULL,
    time_zone         text        NOT NULL DEFAULT 'UTC',
    profile           jsonb       NOT NULL DEFAULT '{}'::jsonb,
    status            text        NOT NULL CHECK (status IN ('active', 'suspended')),
    runtime_retry     text,
    ingest_blocked    text,
    ingest_blocked_at timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, code)
);

CREATE TABLE IF NOT EXISTS credential (
    id                 uuid        PRIMARY KEY,
    store_id           uuid        NOT NULL REFERENCES store (id),
    key_id             text        NOT NULL UNIQUE,
    name               text        NOT NULL,
    role               text        NOT NULL CHECK (role IN ('primary', 'standby')),
    status             text        NOT NULL CHECK (status IN ('active', 'revoked')),
    bus_key_ciphertext bytea       NOT NULL,
    bus_key_nonce      bytea       NOT NULL,
    created_at         timestamptz NOT NULL DEFAULT now(),
    revoked_at         timestamptz
);

CREATE TABLE IF NOT EXISTS hub (
    credential_id uuid        PRIMARY KEY REFERENCES credential (id),
    version       text        NOT NULL DEFAULT '',
    revision      text        NOT NULL DEFAULT '',
    state         jsonb       NOT NULL DEFAULT '{}'::jsonb,
    last_seen_at  timestamptz
);

CREATE TABLE IF NOT EXISTS hub_incident (
    id            uuid        PRIMARY KEY,
    credential_id uuid        NOT NULL REFERENCES credential (id),
    kind          text        NOT NULL,
    scope_hint    text        NOT NULL DEFAULT '',
    opened_at     timestamptz NOT NULL,
    closed_at     timestamptz,
    detail        jsonb       NOT NULL DEFAULT '{}'::jsonb
);

CREATE TABLE IF NOT EXISTS hub_metric (
    credential_id uuid             NOT NULL REFERENCES credential (id),
    name          text             NOT NULL,
    at            timestamptz      NOT NULL,
    value         double precision NOT NULL,
    PRIMARY KEY (credential_id, name, at)
);

CREATE TABLE IF NOT EXISTS package (
    key         text        NOT NULL,
    version     text        NOT NULL,
    kind        text        NOT NULL CHECK (kind IN ('hub', 'module', 'feature')),
    module_key  text,
    descriptor  jsonb       NOT NULL,
    sha256      text        NOT NULL,
    signature   text        NOT NULL,
    storage_key text        NOT NULL,
    status      text        NOT NULL CHECK (status IN ('published', 'yanked')),
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (key, version)
);

CREATE TABLE IF NOT EXISTS release (
    id              uuid        PRIMARY KEY,
    tenant_id       uuid        NOT NULL REFERENCES tenant (id),
    number          int         NOT NULL,
    hub_version     text        NOT NULL,
    packages        jsonb       NOT NULL,
    note            text        NOT NULL DEFAULT '',
    status          text        NOT NULL CHECK (status IN ('applying', 'applied', 'failed')),
    failure         text,
    owner_instance  text,
    applying_until  timestamptz,
    script_results  jsonb       NOT NULL DEFAULT '[]'::jsonb,
    rollback_of     uuid        REFERENCES release (id),
    created_by      text        NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, number)
);

-- Mỗi tenant chỉ một release đang áp
CREATE UNIQUE INDEX IF NOT EXISTS release_one_applying ON release (tenant_id) WHERE status = 'applying';

CREATE TABLE IF NOT EXISTS feature_flag (
    id          uuid        PRIMARY KEY,
    tenant_id   uuid        NOT NULL REFERENCES tenant (id),
    feature_key text        NOT NULL,
    store_id    uuid        REFERENCES store (id),
    enabled     boolean     NOT NULL,
    time_window jsonb,
    settings    jsonb       NOT NULL DEFAULT '{}'::jsonb,
    revision    int         NOT NULL DEFAULT 1,
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- Cờ mặc định của tenant (store_id NULL) và cờ đè theo store: mỗi loại một dòng
CREATE UNIQUE INDEX IF NOT EXISTS feature_flag_tenant ON feature_flag (tenant_id, feature_key) WHERE store_id IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS feature_flag_store ON feature_flag (tenant_id, feature_key, store_id) WHERE store_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS audit (
    id         bigserial   PRIMARY KEY,
    at         timestamptz NOT NULL DEFAULT now(),
    actor      text        NOT NULL,
    action     text        NOT NULL,
    subject    text        NOT NULL,
    tenant_id  uuid,
    store_id   uuid,
    detail     jsonb       NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX IF NOT EXISTS audit_at ON audit (at DESC);
