-- Nền tảng dùng chung mọi DB (hub, tenant, report, master): cấu hình, change capture, outbox/inbox, cursor.
-- Không có role theo schema: module được tách bằng search_path (cookbook 04 §3.5).

CREATE TABLE IF NOT EXISTS platform.settings (
    key   text PRIMARY KEY,
    value text NOT NULL
);

-- Gắn DB với một store đúng một lần (ghép cặp hub / store gốc của tenant); gắn sang store khác thì từ chối
CREATE OR REPLACE FUNCTION platform.bind_store(p_store uuid) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
    v text;
BEGIN
    INSERT INTO platform.settings (key, value) VALUES ('store_id', p_store::text)
    ON CONFLICT (key) DO NOTHING;
    SELECT value INTO v FROM platform.settings WHERE key = 'store_id';
    IF v <> p_store::text THEN
        RAISE EXCEPTION 'database is bound to store %, refusing %', v, p_store USING ERRCODE = 'P0002';
    END IF;
END $$;

-- store_id mặc định của mọi bảng đồng bộ. Hub gắn store_id một lần khi ghép cặp;
-- DB tenant trên cloud chứa nhiều store nên không gắn: Ingest luôn ghi store_id tường minh.
CREATE OR REPLACE FUNCTION platform.store_id() RETURNS uuid
LANGUAGE plpgsql STABLE AS $$
DECLARE
    v text;
BEGIN
    SELECT value INTO v FROM platform.settings WHERE key = 'store_id';
    IF v IS NULL THEN
        RAISE EXCEPTION 'store_id is not bound on this database; write store_id explicitly'
            USING ERRCODE = 'P0001';
    END IF;
    RETURN v::uuid;
END $$;

-- Bảng chỉ dùng tại cửa hàng: không capture, không sang cloud
CREATE TABLE IF NOT EXISTS platform.local_tables (
    schema_name text NOT NULL,
    table_name  text NOT NULL,
    PRIMARY KEY (schema_name, table_name)
);

-- Thay đổi chờ gửi lên cloud. seq cấp dưới advisory lock nên tăng theo thứ tự commit,
-- cloud bỏ được seq <= last_seq mà không sót transaction commit muộn.
CREATE SEQUENCE IF NOT EXISTS platform.changes_seq;
CREATE TABLE IF NOT EXISTS platform.changes (
    seq         bigint      PRIMARY KEY,
    txid        bigint      NOT NULL DEFAULT txid_current(),
    schema_name text        NOT NULL,
    table_name  text        NOT NULL,
    op          text        NOT NULL,
    row_json    jsonb       NOT NULL,
    -- dòng trước khi UPDATE: cloud bỏ qua, audit dùng để tính diff
    old_json    jsonb,
    context     jsonb,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE OR REPLACE FUNCTION platform.capture() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    r   jsonb;
    o   jsonb;
    ctx text;
BEGIN
    IF TG_OP = 'DELETE' THEN
        r := to_jsonb(OLD);
    ELSE
        r := to_jsonb(NEW);
    END IF;
    IF TG_OP = 'UPDATE' THEN
        o := to_jsonb(OLD);
        IF o = r THEN
            RETURN NULL; -- UPDATE không đổi gì (VD: upsert trùng) thì không sinh thay đổi
        END IF;
    END IF;
    ctx := current_setting('aegis.ctx', true);
    PERFORM pg_advisory_xact_lock(hashtext('platform.capture'));
    INSERT INTO platform.changes (seq, schema_name, table_name, op, row_json, old_json, context)
    VALUES (nextval('platform.changes_seq'), TG_TABLE_SCHEMA, TG_TABLE_NAME, TG_OP, r, o, NULLIF(ctx, '')::jsonb);
    RETURN NULL;
END $$;

-- Sự kiện audit không gắn với dòng dữ liệu (đăng nhập, từ chối truy cập...). Dùng chung sequence và khoá
-- với platform.changes nên audit đọc hai bảng theo một thứ tự commit duy nhất. Không đồng bộ lên cloud.
CREATE TABLE IF NOT EXISTS platform.audit_events (
    seq         bigint      PRIMARY KEY,
    txid        bigint      NOT NULL DEFAULT txid_current(),
    schema_name text        NOT NULL,
    kind        text        NOT NULL,
    event       jsonb       NOT NULL,
    context     jsonb,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE OR REPLACE FUNCTION platform.record_event(p_kind text, p_event jsonb) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
    ctx text;
BEGIN
    ctx := current_setting('aegis.ctx', true);
    PERFORM pg_advisory_xact_lock(hashtext('platform.capture'));
    INSERT INTO platform.audit_events (seq, schema_name, kind, event, context)
    VALUES (nextval('platform.changes_seq'), trim(both ' "' from split_part(current_setting('search_path'), ',', 1)), p_kind, p_event, NULLIF(ctx, '')::jsonb);
END $$;

-- Gắn capture cho mọi bảng của schema trừ outbox/inbox và localTables (chỉ chạy trên DB hub)
CREATE OR REPLACE FUNCTION platform.track_schema(p_schema text) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
    t record;
BEGIN
    FOR t IN
        SELECT c.relname
        FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE n.nspname = p_schema AND c.relkind IN ('r', 'p')
          AND c.relname NOT IN ('outbox', 'inbox')
          AND NOT EXISTS (SELECT 1 FROM platform.local_tables l WHERE l.schema_name = p_schema AND l.table_name = c.relname)
          AND NOT EXISTS (SELECT 1 FROM pg_trigger g WHERE g.tgrelid = c.oid AND g.tgname = 'platform_capture')
    LOOP
        EXECUTE format('CREATE TRIGGER platform_capture AFTER INSERT OR UPDATE OR DELETE ON %I.%I FOR EACH ROW EXECUTE FUNCTION platform.capture()', p_schema, t.relname);
    END LOOP;
END $$;

-- seq outbox cấp dưới advisory lock của schema: tăng theo thứ tự commit, dispatcher đọc theo cursor không sót
CREATE OR REPLACE FUNCTION platform.next_in_commit_order() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_advisory_xact_lock(hashtext(TG_TABLE_SCHEMA || '.outbox'));
    NEW.seq := nextval(format('%I.outbox_seq', TG_TABLE_SCHEMA));
    RETURN NEW;
END $$;

-- Tạo schema của gói kèm outbox (sự kiện phát ra, cùng transaction nghiệp vụ) và inbox (khử trùng khi nhận)
CREATE OR REPLACE FUNCTION platform.provision_schema(p_schema text, p_local_tables text[]) RETURNS void
LANGUAGE plpgsql AS $$
BEGIN
    IF p_schema !~ '^(biz|ft)_[a-z0-9_]+$' OR length(p_schema) > 57 THEN
        RAISE EXCEPTION 'invalid package schema name: %', p_schema;
    END IF;
    EXECUTE format('CREATE SCHEMA IF NOT EXISTS %I', p_schema);
    EXECUTE format('CREATE SEQUENCE IF NOT EXISTS %I.outbox_seq', p_schema);
    EXECUTE format($f$
        CREATE TABLE IF NOT EXISTS %I.outbox (
            seq         bigint      PRIMARY KEY,
            event_id    text        NOT NULL UNIQUE,
            type        text        NOT NULL,
            payload     bytea       NOT NULL,
            txid        bigint      NOT NULL DEFAULT txid_current(),
            created_at  timestamptz NOT NULL DEFAULT now()
        )$f$, p_schema);
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid = format('%I.outbox', p_schema)::regclass AND tgname = 'platform_outbox_seq') THEN
        EXECUTE format('CREATE TRIGGER platform_outbox_seq BEFORE INSERT ON %I.outbox FOR EACH ROW EXECUTE FUNCTION platform.next_in_commit_order()', p_schema);
    END IF;
    EXECUTE format($f$
        CREATE TABLE IF NOT EXISTS %I.inbox (
            event_id    text        PRIMARY KEY,
            received_at timestamptz NOT NULL DEFAULT now()
        )$f$, p_schema);
    INSERT INTO platform.local_tables (schema_name, table_name)
    SELECT p_schema, unnest(p_local_tables)
    ON CONFLICT DO NOTHING;
END $$;

-- Cursor của Ingest trên DB tenant: mỗi store một dòng; epoch đổi khi DB hub được dựng lại
CREATE TABLE IF NOT EXISTS platform.cursors (
    store_id   uuid        PRIMARY KEY,
    epoch      text        NOT NULL,
    last_seq   bigint      NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS platform.retired_epochs (
    store_id   uuid        NOT NULL,
    epoch      text        NOT NULL,
    retired_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (store_id, epoch)
);

-- Cursor của consumer đọc theo seq: OutboxDispatcher (subscriber=module, publisher=<schema>),
-- change feed (subscriber=@audit / @cloud, publisher=platform). platform.changes chỉ được xoá
-- tới cursor nhỏ nhất của các consumer publisher=platform.
CREATE TABLE IF NOT EXISTS platform.dispatch_cursors (
    subscriber text   NOT NULL,
    publisher  text   NOT NULL,
    last_seq   bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (subscriber, publisher)
);

CREATE TABLE IF NOT EXISTS platform.dead_letters (
    id         bigserial   PRIMARY KEY,
    subscriber text        NOT NULL,
    publisher  text        NOT NULL,
    event_id   text        NOT NULL,
    attempts   int         NOT NULL,
    last_error text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
