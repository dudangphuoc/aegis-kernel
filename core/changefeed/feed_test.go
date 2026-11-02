package changefeed_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dudangphuoc/aegis-kernel/core/changefeed"
	"github.com/dudangphuoc/aegis-kernel/core/contract"
	authdb "github.com/dudangphuoc/aegis-kernel/modules/auth/db"
	"github.com/dudangphuoc/aegis-kernel/sdk/audit"
	"github.com/dudangphuoc/aegis-kernel/sdk/multitenant"
	"github.com/dudangphuoc/aegis-kernel/sdk/tenantdb"
	"github.com/dudangphuoc/aegis-kernel/sdk/tenantdb/tenanttest"
)

type source struct{ dsn string }

func (s source) TenantIDs(context.Context) ([]string, error) { return []string{"acme"}, nil }
func (s source) TenantDatabaseDSN(context.Context, string) (string, error) {
	return s.dsn, nil
}

type publisher struct {
	mu   sync.Mutex
	envs []audit.Envelope
	fail bool
}

func (p *publisher) Publish(_ context.Context, ev contract.Event) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail {
		return errors.New("audit module down")
	}
	var env audit.Envelope
	if err := json.Unmarshal(ev.Payload, &env); err != nil {
		return err
	}
	if ev.ID != env.ID || ev.Topic != contract.TopicAuditRecorded {
		return errors.New("event id/topic mismatch")
	}
	p.envs = append(p.envs, env)
	return nil
}

func setup(t *testing.T) (context.Context, *tenantdb.DB, *sql.DB, *changefeed.Feed, *publisher) {
	t.Helper()
	pkg, err := authdb.Package()
	if err != nil {
		t.Fatal(err)
	}
	adminDSN, runtimeDSN := tenanttest.FreshTenant(t, "tenant_feed_test", pkg)
	raw, err := sql.Open("postgres", adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.Close() })

	dbm := tenantdb.NewManager("biz_auth")
	t.Cleanup(func() { _ = dbm.Close(context.Background()) })
	ctx := multitenant.WithTenant(context.Background(), contract.TenantRequestContext{TenantID: "acme", ConnectionString: runtimeDSN})
	ctx = tenantdb.WithRequestContext(ctx, &tenantdb.RequestContext{TenantID: "acme", RequestID: "req-1", SourceModule: "auth",
		Method: "POST", Path: "/api/v1/users", Action: "CREATE_USER", Actor: audit.Actor{ID: "USR-ADMIN", Role: "TENANT_ADMIN"}})
	db, _, err := dbm.Resolve(ctx)
	if err != nil {
		t.Fatal(err)
	}

	pub := &publisher{}
	feed := changefeed.New(source{dsn: runtimeDSN}, pub, slog.Default())
	feed.SetPolicies(map[string][]contract.AuditTable{
		"auth": {{Table: "users", Entity: "UserAccount", PK: "id", Ignore: []string{"store_id"}, Secrets: []string{"password", "mfa_code"}}},
	})
	t.Cleanup(func() { _ = feed.Stop(context.Background()) })
	return ctx, db, raw, feed, pub
}

func exec(t *testing.T, ctx context.Context, db *tenantdb.DB, queries ...string) {
	t.Helper()
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		for _, q := range queries {
			if _, err := tx.ExecContext(ctx, q); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestFeedBuildsEnvelopesPerTransactionWithRedaction(t *testing.T) {
	ctx, db, raw, feed, pub := setup(t)

	// tx1: 2 dòng; tx2: đổi mật khẩu + role; tx3: sự kiện bảo mật; tx4: ghi thẳng bằng psql (không qua SDK)
	exec(t, ctx, db,
		`INSERT INTO users (id, tenant_id, username, password) VALUES ('U1', 'acme', 'an', 'secret-1')`,
		`INSERT INTO users (id, tenant_id, username, password) VALUES ('U2', 'acme', 'binh', 'secret-2')`)
	exec(t, ctx, db, `UPDATE users SET password = 'secret-9', role = 'STAFF' WHERE id = 'U1'`)
	exec(t, ctx, db, `UPDATE users SET role = 'STAFF' WHERE id = 'U1'`) // không đổi gì: không sinh audit
	if err := db.RecordSecurityEvent(ctx, "LOGIN_FAILED", "binh", "bad password"); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`DELETE FROM biz_auth.users WHERE id = 'U2'`); err != nil {
		t.Fatal(err)
	}

	if more, err := feed.Drain(context.Background(), "acme"); err != nil || more {
		t.Fatalf("drain: more=%v err=%v", more, err)
	}
	if len(pub.envs) != 4 {
		t.Fatalf("expected 4 envelopes (one per transaction), got %d", len(pub.envs))
	}
	insert, update, event, bypass := pub.envs[0], pub.envs[1], pub.envs[2], pub.envs[3]

	if len(insert.Changes) != 2 || insert.Actor.ID != "USR-ADMIN" || insert.ActionName != "CREATE_USER" || insert.RequestID != "req-1" || insert.SourceModule != "auth" {
		t.Fatalf("insert envelope: %+v", insert)
	}
	c := insert.Changes[0]
	if c.EntityName != "UserAccount" || c.EntityID != "U1" || c.After["password"] != "[REDACTED]" || c.After["store_id"] != nil || c.After["username"] != "an" {
		t.Fatalf("insert change: %+v", c)
	}

	u := update.Changes[0]
	if u.Operation != audit.OpUpdate || u.Before["password"] != "[REDACTED]" || u.After["password"] != "[REDACTED:CHANGED]" ||
		u.Before["role"] != "TENANT_ADMIN" || u.After["role"] != "STAFF" {
		t.Fatalf("update change: %+v", u)
	}
	if len(event.Events) != 1 || event.Events[0].Name != "LOGIN_FAILED" || len(event.Changes) != 0 {
		t.Fatalf("event envelope: %+v", event)
	}
	if bypass.Actor.ID != "SYSTEM" || bypass.Changes[0].Operation != audit.OpDelete || bypass.Changes[0].Before["username"] != "binh" {
		t.Fatalf("bypass envelope: %+v", bypass)
	}

	// Cursor đã tiến, dữ liệu đã giao bị dọn; lần sau không giao lại
	var changes, events int
	_ = raw.QueryRow(`SELECT (SELECT count(*) FROM platform.changes), (SELECT count(*) FROM platform.audit_events)`).Scan(&changes, &events)
	if changes != 0 || events != 0 {
		t.Fatalf("expected pruned feed tables, got changes=%d events=%d", changes, events)
	}
	if _, err := feed.Drain(context.Background(), "acme"); err != nil || len(pub.envs) != 4 {
		t.Fatalf("second drain must deliver nothing: err=%v envs=%d", err, len(pub.envs))
	}
}

func TestFeedKeepsRowsWhenSubscriberFailsAndRedeliversSameIDs(t *testing.T) {
	ctx, db, raw, feed, pub := setup(t)
	exec(t, ctx, db, `INSERT INTO users (id, tenant_id, username, password) VALUES ('U1', 'acme', 'an', 'x')`)

	pub.fail = true
	if _, err := feed.Drain(context.Background(), "acme"); err == nil {
		t.Fatal("expected publish error")
	}
	var n int
	_ = raw.QueryRow(`SELECT count(*) FROM platform.changes`).Scan(&n)
	if n != 1 {
		t.Fatalf("rows must stay until delivered, got %d", n)
	}

	pub.fail = false
	if _, err := feed.Drain(context.Background(), "acme"); err != nil {
		t.Fatal(err)
	}
	if len(pub.envs) != 1 || pub.envs[0].ID == "" {
		t.Fatalf("expected redelivery, got %+v", pub.envs)
	}
}

func TestFeedForgetsDroppedTenant(t *testing.T) {
	_, _, raw, feed, _ := setup(t)
	raw.Close()
	admin, err := sql.Open("postgres", "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err := admin.Exec(`DROP DATABASE tenant_feed_test WITH (FORCE)`); err != nil {
		t.Fatal(err)
	}
	if _, err := feed.Drain(context.Background(), "acme"); err == nil {
		t.Fatal("expected error for dropped database")
	}
}

type goneSource struct{}

func (goneSource) TenantIDs(context.Context) ([]string, error) { return []string{"gone"}, nil }
func (goneSource) TenantDatabaseDSN(context.Context, string) (string, error) {
	return "", contract.ErrTenantNotFound
}

// Tenant đã bị xoá khỏi Control Plane: feed bỏ hẳn, không thử lại mỗi 2 giây
func TestFeedDropsTenantRemovedFromControlPlane(t *testing.T) {
	var logs bytes.Buffer
	feed := changefeed.New(goneSource{}, &publisher{}, slog.New(slog.NewTextHandler(&logs, nil)))
	feed.Start()
	time.Sleep(2500 * time.Millisecond) // quá một chu kỳ retry
	if err := feed.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(logs.String(), "dropping it from change feed"); n != 1 {
		t.Fatalf("expected tenant dropped exactly once, got %d:\n%s", n, logs.String())
	}
	if strings.Contains(logs.String(), "drain failed") {
		t.Fatalf("removed tenant must not be retried:\n%s", logs.String())
	}
}
