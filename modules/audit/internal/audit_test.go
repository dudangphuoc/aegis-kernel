package test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/dudangphuoc/aegis-kernel/core/contract"
	auditdb "github.com/dudangphuoc/aegis-kernel/modules/audit/db"
	"github.com/dudangphuoc/aegis-kernel/modules/audit/internal/adapter/storage"
	"github.com/dudangphuoc/aegis-kernel/modules/audit/internal/domain"
	"github.com/dudangphuoc/aegis-kernel/sdk/audit"
	"github.com/dudangphuoc/aegis-kernel/sdk/multitenant"
	"github.com/dudangphuoc/aegis-kernel/sdk/tenantdb"
	"github.com/dudangphuoc/aegis-kernel/sdk/tenantdb/tenanttest"
)

// setup tạo DB sạch, ghi 3 envelope (INSERT, UPDATE, sự kiện đăng nhập) và trả về repo + kết nối thô để giả mạo
func setup(t *testing.T) (context.Context, *storage.PostgresAuditRepo, *sql.DB) {
	t.Helper()
	pkg, err := auditdb.Package()
	if err != nil {
		t.Fatal(err)
	}
	adminDSN, runtimeDSN := tenanttest.FreshTenant(t, "tenant_audit_test", pkg)
	raw, err := sql.Open("postgres", adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	raw.SetMaxOpenConns(1) // một session duy nhất giữ search_path cho các câu giả mạo trong test
	if _, err := raw.Exec(`SET search_path TO biz_audit`); err != nil {
		t.Fatal(err)
	}

	dbm := tenantdb.NewManager("biz_audit")
	t.Cleanup(func() { _ = dbm.Close(context.Background()) })
	repo := storage.NewPostgresAuditRepo(dbm)
	ctx := multitenant.WithTenant(context.Background(), contract.TenantRequestContext{TenantID: "tenant-test", ConnectionString: runtimeDSN})

	now := time.Now()
	actor := audit.Actor{ID: "USR-1", Username: "admin", Role: "TENANT_ADMIN"}
	envs := []audit.Envelope{
		{ID: "AUD-1", TenantID: "tenant-test", SourceModule: "parties", ActionMethod: "POST", ActionPath: "/api/v1/parties",
			ActionName: "REGISTER_PARTY", StatusCode: 201, Actor: actor, OccurredAt: now,
			Client: audit.ClientInfo{IP: "198.51.100.7", Details: map[string]string{"app": "mobile", "device_id": "D-1"}},
			Changes: []audit.EntityChange{{TableName: "parties", EntityName: "Party", EntityID: "P1", Operation: audit.OpInsert,
				After: map[string]any{"id": "P1", "full_name": "Nguyen A", "email": "a@x.com"}, ChangedAt: now}}},
		{ID: "AUD-2", TenantID: "tenant-test", SourceModule: "parties", ActionMethod: "POST", ActionPath: "/api/v1/parties",
			ActionName: "REGISTER_PARTY", StatusCode: 201, Actor: actor, OccurredAt: now.Add(time.Second),
			Changes: []audit.EntityChange{{TableName: "parties", EntityName: "Party", EntityID: "P1", Operation: audit.OpUpdate,
				Before:    map[string]any{"id": "P1", "full_name": "Nguyen A", "email": "a@x.com"},
				After:     map[string]any{"id": "P1", "full_name": "Nguyen B", "email": "a@x.com"},
				ChangedAt: now.Add(time.Second)}}},
		{ID: "AUD-3", TenantID: "tenant-test", SourceModule: "auth", ActionMethod: "POST", ActionPath: "/api/v1/auth/login",
			ActionName: "LOGIN", StatusCode: 401, Actor: audit.Actor{ID: "ANONYMOUS"}, OccurredAt: now.Add(2 * time.Second),
			Events: []audit.SecurityEvent{{Name: "LOGIN_FAILED", Subject: "admin", OccurredAt: now}}},
	}
	for _, e := range envs {
		if err := repo.Ingest(ctx, e); err != nil {
			t.Fatalf("ingest %s: %v", e.ID, err)
		}
	}
	return ctx, repo, raw
}

func TestAuditLedger_ThreeLayersAndValidChain(t *testing.T) {
	ctx, repo, raw := setup(t)

	// Giao lại (at-least-once) không được tạo trùng
	if err := repo.Ingest(ctx, audit.Envelope{ID: "AUD-1", TenantID: "tenant-test"}); err != nil {
		t.Fatalf("re-ingest must be idempotent: %v", err)
	}

	views, err := repo.List(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 3 {
		t.Fatalf("expected 3 audits, got %d", len(views))
	}
	byID := map[string]domain.AuditView{}
	for _, v := range views {
		byID[v.ID] = v
	}
	if n := len(byID["AUD-1"].FieldLogs); n != 3 {
		t.Fatalf("INSERT must log every field, got %d", n)
	}
	upd := byID["AUD-2"]
	if len(upd.Changes) != 1 || len(upd.FieldLogs) != 1 || *upd.FieldLogs[0].FieldName != "full_name" ||
		*upd.FieldLogs[0].OldValue != `"Nguyen A"` || *upd.FieldLogs[0].NewValue != `"Nguyen B"` {
		t.Fatalf("unexpected UPDATE diff: %+v", upd.FieldLogs)
	}
	if ci := string(byID["AUD-1"].ClientInfo); ci != `{"app":"mobile","device_id":"D-1"}` {
		t.Fatalf("client_info not stored: %s", ci)
	}
	if byID["AUD-3"].ClientInfo != nil {
		t.Fatalf("empty client_info must be omitted, got %s", byID["AUD-3"].ClientInfo)
	}
	if seal := byID["AUD-3"].FieldLogs; len(seal) != 1 || seal[0].FieldName != nil || seal[0].EntityChangeID != nil {
		t.Fatalf("event-only audit must be sealed by one chain row: %+v", seal)
	}

	report, err := repo.VerifyIntegrity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "VALID" || report.CheckedLogs != 5 {
		t.Fatalf("expected VALID chain of 5 logs, got %+v", report)
	}
	var count int
	_ = raw.QueryRow(`SELECT count(*) FROM audits`).Scan(&count)
	if count != 3 {
		t.Fatalf("idempotency broken: %d audits", count)
	}
}

func TestAuditLedger_DetectsTampering(t *testing.T) {
	cases := map[string]string{
		"layer1 actor edited":       `UPDATE audits SET actor_id = 'USR-EVIL' WHERE id = 'AUD-1'`,
		"layer1 client_info edited": `UPDATE audits SET client_info = '{"app":"forged"}'::jsonb WHERE id = 'AUD-1'`,
		"layer1 client_info added":  `UPDATE audits SET client_info = '{"app":"forged"}'::jsonb WHERE id = 'AUD-3'`,
		"layer1 events edited":      `UPDATE audits SET security_events = '[]'::jsonb WHERE id = 'AUD-3'`,
		"layer2 snapshot edited":    `UPDATE entity_changes SET after_snapshot = jsonb_set(after_snapshot, '{full_name}', '"Hacker"') WHERE audit_id = 'AUD-2'`,
		"layer3 value edited":       `UPDATE entity_audit_logs SET new_value = '"Hacker"' WHERE audit_id = 'AUD-2'`,
		"layer3 row deleted":        `DELETE FROM entity_audit_logs WHERE seq = 2`,
		"layer3 hash recomputed?":   `UPDATE entity_audit_logs SET integrity_hash = repeat('a', 64) WHERE seq = 4`,
	}
	for name, stmt := range cases {
		t.Run(name, func(t *testing.T) {
			ctx, repo, raw := setup(t)
			if _, err := raw.Exec(stmt); err != nil {
				t.Fatal(err)
			}
			report, err := repo.VerifyIntegrity(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if report.Status != "COMPROMISED" {
				t.Fatalf("tampering not detected: %+v", report)
			}
		})
	}
}

func TestAuditLedger_RejectsForeignTenant(t *testing.T) {
	ctx, repo, _ := setup(t)
	err := repo.Ingest(ctx, audit.Envelope{ID: "AUD-X", TenantID: "other-tenant"})
	if !errors.Is(err, domain.ErrTenantMismatch) {
		t.Fatalf("expected ErrTenantMismatch, got %v", err)
	}
}

func TestAuditLedger_ConcurrentIngestKeepsSingleChain(t *testing.T) {
	ctx, repo, _ := setup(t)
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		go func(i int) {
			errs <- repo.Ingest(ctx, audit.Envelope{
				ID: "AUD-C-" + string(rune('a'+i)), TenantID: "tenant-test", SourceModule: "auth",
				ActionMethod: "POST", ActionPath: "/x", StatusCode: 200, Actor: audit.Actor{ID: "ANONYMOUS"}, OccurredAt: time.Now(),
				Events: []audit.SecurityEvent{{Name: "LOGIN_SUCCESS", OccurredAt: time.Now()}},
			})
		}(i)
	}
	for i := 0; i < 20; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	report, err := repo.VerifyIntegrity(ctx)
	if err != nil || report.Status != "VALID" || report.CheckedLogs != 25 {
		t.Fatalf("concurrent ingest forked the chain: %+v %v", report, err)
	}
}
