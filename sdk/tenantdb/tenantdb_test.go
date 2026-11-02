package tenantdb_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/dudangphuoc/aegis-kernel/core/contract"
	"github.com/dudangphuoc/aegis-kernel/framework/core/data/scripts"
	"github.com/dudangphuoc/aegis-kernel/sdk/audit"
	"github.com/dudangphuoc/aegis-kernel/sdk/multitenant"
	"github.com/dudangphuoc/aegis-kernel/sdk/tenantdb"
	"github.com/dudangphuoc/aegis-kernel/sdk/tenantdb/tenanttest"
)

func pkg(key string) scripts.Package {
	return scripts.Package{Name: key, Schema: scripts.ModuleSchema(key), Profile: scripts.ProfileModule, Scripts: []scripts.Script{
		scripts.NewScript("202611021000_items.sql", `CREATE TABLE IF NOT EXISTS items (
			id text NOT NULL,
			store_id uuid NOT NULL DEFAULT platform.store_id(),
			name text NOT NULL,
			PRIMARY KEY (store_id, id)
		)`),
	}}
}

func setup(t *testing.T) (context.Context, *tenantdb.Manager, *tenantdb.Manager, *sql.DB) {
	t.Helper()
	adminDSN, runtimeDSN := tenanttest.FreshTenant(t, "aegis_tenantdb_test", pkg("widgets"), pkg("gadgets"))
	raw, err := sql.Open("postgres", adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.Close() })
	widgets, gadgets := tenantdb.NewManager("biz_widgets"), tenantdb.NewManager("biz_gadgets")
	t.Cleanup(func() { _ = widgets.Close(context.Background()); _ = gadgets.Close(context.Background()) })
	ctx := multitenant.WithTenant(context.Background(), contract.TenantRequestContext{TenantID: "acme", ConnectionString: runtimeDSN})
	ctx = tenantdb.WithRequestContext(ctx, &tenantdb.RequestContext{TenantID: "acme", SourceModule: "widgets", Actor: audit.Actor{ID: "USR-7"}})
	return ctx, widgets, gadgets, raw
}

func TestSchemaIsolationBySearchPath(t *testing.T) {
	ctx, widgets, gadgets, raw := setup(t)
	write := func(m *tenantdb.Manager, name string) {
		db, _, err := m.Resolve(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Write(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO items (id, name) VALUES ('1', $1)`, name)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Cùng tên bảng "items" nhưng mỗi module ghi vào schema của mình
	write(widgets, "widget")
	write(gadgets, "gadget")
	for schema, want := range map[string]string{"biz_widgets": "widget", "biz_gadgets": "gadget"} {
		var got string
		if err := raw.QueryRow(`SELECT name FROM ` + schema + `.items`).Scan(&got); err != nil || got != want {
			t.Fatalf("%s.items = %q (%v), want %q", schema, got, err, want)
		}
	}
	// Mỗi thay đổi capture mang ngữ cảnh request (actor)
	var actor string
	if err := raw.QueryRow(`SELECT context->'actor'->>'id' FROM platform.changes ORDER BY seq LIMIT 1`).Scan(&actor); err != nil || actor != "USR-7" {
		t.Fatalf("captured actor = %q (%v)", actor, err)
	}
}

func TestReadIsReadOnlyAndSecurityEventsAreOrdered(t *testing.T) {
	ctx, widgets, _, raw := setup(t)
	db, _, err := widgets.Resolve(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Read(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO items (id, name) VALUES ('x', 'x')`)
		return err
	}); err == nil {
		t.Fatal("write inside Read must fail")
	}

	if err := db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO items (id, name) VALUES ('2', 'two')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordSecurityEvent(ctx, "LOGIN_FAILED", "admin", "bad password"); err != nil {
		t.Fatal(err)
	}
	// Sự kiện và thay đổi dùng chung một dãy seq theo thứ tự commit
	var changeSeq, eventSeq int64
	var schema, name string
	if err := raw.QueryRow(`SELECT max(seq) FROM platform.changes`).Scan(&changeSeq); err != nil {
		t.Fatal(err)
	}
	if err := raw.QueryRow(`SELECT seq, schema_name, event->>'name' FROM platform.audit_events`).Scan(&eventSeq, &schema, &name); err != nil {
		t.Fatal(err)
	}
	if eventSeq <= changeSeq || schema != "biz_widgets" || name != "LOGIN_FAILED" {
		t.Fatalf("event seq=%d (change seq=%d) schema=%s name=%s", eventSeq, changeSeq, schema, name)
	}
}
