// Package dbaudit ghi nhận thay đổi dữ liệu TẠI TẦNG DATABASE:
//   - Trigger Postgres trên từng bảng ghi before/after (jsonb) vào aegis_audit_outbox trong CÙNG transaction
//     với dữ liệu nghiệp vụ, nên mọi lệnh ghi (qua API, seed, script, psql) đều bị bắt và không thể mất khi crash.
//   - DB wrapper gắn ngữ cảnh request (actor, IP, request id) vào transaction qua set_config(..., true).
//   - Relay đọc outbox, gom theo transaction thành audit.Envelope, đẩy qua Kernel Bus và chỉ xóa sau khi được ack.
package dbaudit

import (
	"fmt"
	"strings"

	"github.com/dudangphuoc/aegis-kernel/sdk/migrator"
)

// contextSetting là biến cấu hình theo transaction mà trigger đọc để lấy ngữ cảnh request
const contextSetting = "aegis.audit_context"

const (
	// RedactedValue thay giá trị cột bí mật; RedactedChanged báo cột bí mật đã đổi mà không lộ giá trị
	RedactedValue   = "[REDACTED]"
	RedactedChanged = "[REDACTED:CHANGED]"
)

// Table mô tả 1 bảng được audit
type Table struct {
	Name    string   // tên bảng vật lý
	Entity  string   // tên domain entity (VD: Party)
	PK      string   // cột khóa chính
	Ignore  []string // cột hạ tầng không đưa vào audit (VD: processed_by)
	Secrets []string // cột bí mật: chỉ ghi nhận "có đổi", không lưu giá trị (VD: password)
}

const installSQL = `
CREATE TABLE IF NOT EXISTS aegis_audit_outbox (
    seq BIGSERIAL PRIMARY KEY,
    txid BIGINT NOT NULL DEFAULT txid_current(),
    kind VARCHAR(16) NOT NULL CHECK (kind IN ('CHANGE', 'EVENT')),
    table_name VARCHAR(128),
    entity_name VARCHAR(128),
    entity_id VARCHAR(256),
    operation VARCHAR(16),
    before_row JSONB,
    after_row JSONB,
    event JSONB,
    context JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

CREATE OR REPLACE FUNCTION aegis_audit_capture() RETURNS trigger
LANGUAGE plpgsql AS $fn$
DECLARE
    entity  text   := TG_ARGV[0];
    pk      text   := TG_ARGV[1];
    ignored text[] := COALESCE(string_to_array(NULLIF(TG_ARGV[2], ''), ','), '{}'::text[]);
    secrets text[] := COALESCE(string_to_array(NULLIF(TG_ARGV[3], ''), ','), '{}'::text[]);
    old_row jsonb;
    new_row jsonb;
    col     text;
BEGIN
    IF TG_OP IN ('UPDATE', 'DELETE') THEN
        old_row := to_jsonb(OLD) - ignored;
    END IF;
    IF TG_OP IN ('INSERT', 'UPDATE') THEN
        new_row := to_jsonb(NEW) - ignored;
    END IF;

    FOREACH col IN ARRAY secrets LOOP
        IF old_row ? col THEN
            old_row := jsonb_set(old_row, ARRAY[col], to_jsonb('` + RedactedValue + `'::text));
        END IF;
        IF new_row ? col THEN
            IF TG_OP = 'UPDATE' AND (to_jsonb(OLD) -> col) IS DISTINCT FROM (to_jsonb(NEW) -> col) THEN
                new_row := jsonb_set(new_row, ARRAY[col], to_jsonb('` + RedactedChanged + `'::text));
            ELSE
                new_row := jsonb_set(new_row, ARRAY[col], to_jsonb('` + RedactedValue + `'::text));
            END IF;
        END IF;
    END LOOP;

    -- UPDATE không đổi giá trị nào (sau khi loại cột hạ tầng) thì không phải thay đổi dữ liệu
    IF TG_OP = 'UPDATE' AND old_row = new_row THEN
        RETURN NULL;
    END IF;

    INSERT INTO aegis_audit_outbox (kind, table_name, entity_name, entity_id, operation, before_row, after_row, context)
    VALUES ('CHANGE', TG_TABLE_NAME, entity, COALESCE(new_row, old_row) ->> pk, TG_OP, old_row, new_row,
            NULLIF(current_setting('` + contextSetting + `', true), '')::jsonb);
    RETURN NULL;
END
$fn$;
`

// Migration tạo outbox + hàm trigger và gắn trigger cho các bảng. Đặt vào danh sách migration của module.
func Migration(version int, tables ...Table) migrator.MigrationScript {
	var up, down strings.Builder
	up.WriteString(installSQL)
	for _, t := range tables {
		trigger := triggerName(t.Name)
		fmt.Fprintf(&up, "\nDROP TRIGGER IF EXISTS %s ON %s;\n", trigger, t.Name)
		fmt.Fprintf(&up, "CREATE TRIGGER %s AFTER INSERT OR UPDATE OR DELETE ON %s FOR EACH ROW EXECUTE FUNCTION aegis_audit_capture(%s, %s, %s, %s);\n",
			trigger, t.Name, quote(t.Entity), quote(t.PK), quote(strings.Join(t.Ignore, ",")), quote(strings.Join(t.Secrets, ",")))
		fmt.Fprintf(&down, "DROP TRIGGER IF EXISTS %s ON %s;\n", trigger, t.Name)
	}
	down.WriteString("DROP FUNCTION IF EXISTS aegis_audit_capture();\nDROP TABLE IF EXISTS aegis_audit_outbox;\n")
	return migrator.MigrationScript{Version: version, Name: "install_db_audit", UpSQL: up.String(), DownSQL: down.String()}
}

func triggerName(table string) string { return "aegis_audit_" + table }

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
