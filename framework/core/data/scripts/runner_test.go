package scripts_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"

	_ "github.com/lib/pq"

	"github.com/dudangphuoc/aegis-kernel/db"
	"github.com/dudangphuoc/aegis-kernel/framework/core/data/scripts"
)

// freshDB tạo DB trống cho mỗi test (Postgres trực tiếp :5432; bỏ qua nếu không kết nối được)
func freshDB(t *testing.T, name string) *sql.DB {
	t.Helper()
	admin := os.Getenv("TEST_PG_ADMIN_DSN")
	if admin == "" {
		admin = "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable"
	}
	a, err := sql.Open("postgres", admin)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.Ping(); err != nil {
		t.Skipf("postgres not available: %v", err)
	}
	if _, err := a.Exec(`DROP DATABASE IF EXISTS "` + name + `" WITH (FORCE)`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Exec(`CREATE DATABASE "` + name + `"`); err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("postgres", strings.Replace(admin, "/postgres?", "/"+name+"?", 1))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func platformPkg(t *testing.T) scripts.Package {
	t.Helper()
	p, err := db.Platform("test")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func partiesPkg(extra ...scripts.Script) scripts.Package {
	s := []scripts.Script{
		scripts.NewScript("202611020900_party.sql", `CREATE TABLE IF NOT EXISTS party (
			id uuid NOT NULL,
			store_id uuid NOT NULL DEFAULT platform.store_id(),
			name text NOT NULL,
			PRIMARY KEY (store_id, id)
		);
		CREATE TABLE IF NOT EXISTS print_queue (id bigserial PRIMARY KEY, body text NOT NULL);`),
	}
	return scripts.Package{Name: "parties", Version: "1.0.0", Schema: scripts.ModuleSchema("parties"),
		Profile: scripts.ProfileModule, LocalTables: []string{"print_queue"}, Scripts: append(s, extra...)}
}

func TestRunnerAppliesPlatformAndModuleIntoSchemas(t *testing.T) {
	ctx := context.Background()
	conn := freshDB(t, "aegis_scripts_hub_test")
	r := &scripts.Runner{DB: conn, TrackChanges: true}

	master, _ := db.Master("test")
	plans, err := r.Apply(ctx, platformPkg(t), master, partiesPkg())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range plans {
		if p.Count(scripts.StatusPending) != 0 {
			t.Fatalf("%s still has pending scripts: %+v", p.Package, p)
		}
	}

	// Lần 2: không áp lại gì
	again, err := r.Plan(ctx, platformPkg(t), partiesPkg())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range again {
		if p.Count(scripts.StatusApplied) != len(p.Scripts) {
			t.Fatalf("expected all applied on re-plan: %+v", p)
		}
	}

	// Bảng nằm trong schema của module; outbox/inbox được cấp sẵn
	var n int
	if err := conn.QueryRow(`SELECT count(*) FROM information_schema.tables WHERE table_schema = 'biz_parties' AND table_name IN ('party', 'print_queue', 'outbox', 'inbox')`).Scan(&n); err != nil || n != 4 {
		t.Fatalf("expected 4 tables in biz_parties, got %d (%v)", n, err)
	}

	// Chưa gắn store: ghi thiếu store_id phải lỗi (DB tenant trên cloud)
	if _, err := conn.Exec(`INSERT INTO biz_parties.party (id, name) VALUES (gen_random_uuid(), 'x')`); err == nil {
		t.Fatal("insert without bound store_id must fail")
	}

	// Gắn store (hub): store_id tự điền, thay đổi được capture theo thứ tự; bảng local thì không
	if _, err := conn.Exec(`INSERT INTO platform.settings (key, value) VALUES ('store_id', '11111111-1111-1111-1111-111111111111')`); err != nil {
		t.Fatal(err)
	}
	tx, _ := conn.Begin()
	if _, err := tx.Exec(`SELECT set_config('aegis.ctx', '{"actor":"USR-1"}', true)`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`SET LOCAL search_path TO biz_parties, platform`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO party (id, name) VALUES ('22222222-2222-2222-2222-222222222222', 'An'); UPDATE party SET name = 'Bình'; INSERT INTO print_queue (body) VALUES ('p')`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	rows, err := conn.Query(`SELECT op, table_name, row_json->>'name', row_json->>'store_id', context->>'actor' FROM platform.changes ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var op, table, name, store, actor string
		if err := rows.Scan(&op, &table, &name, &store, &actor); err != nil {
			t.Fatal(err)
		}
		got = append(got, op+" "+table+" "+name+" "+store[:8]+" "+actor)
	}
	want := []string{"INSERT party An 11111111 USR-1", "UPDATE party Bình 11111111 USR-1"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("captured changes = %v, want %v", got, want)
	}
}

func TestRunnerDetectsEditedAndAheadScripts(t *testing.T) {
	ctx := context.Background()
	conn := freshDB(t, "aegis_scripts_ledger_test")
	r := &scripts.Runner{DB: conn}
	idx := scripts.NewScript("202611020901_idx.sql", "-- platform:concurrently\nCREATE INDEX CONCURRENTLY IF NOT EXISTS party_name ON party (store_id, name)")
	if _, err := r.Apply(ctx, platformPkg(t), partiesPkg(idx)); err != nil {
		t.Fatal(err)
	}
	var indexed bool
	if err := conn.QueryRow(`SELECT to_regclass('biz_parties.party_name') IS NOT NULL`).Scan(&indexed); err != nil || !indexed {
		t.Fatalf("concurrently index not created (%v)", err)
	}

	// Hạ version: package không còn script idx => sổ đi trước (ahead), không phải lỗi
	plans, err := r.Plan(ctx, partiesPkg())
	if err != nil {
		t.Fatal(err)
	}
	if plans[0].Count(scripts.StatusAhead) != 1 {
		t.Fatalf("expected 1 ahead script: %+v", plans[0])
	}

	// Sửa file đã áp => migration-edited, không áp gì thêm
	edited := partiesPkg(idx)
	edited.Scripts[0] = scripts.NewScript(edited.Scripts[0].Name, edited.Scripts[0].SQL+"\n-- edited")
	edited.Scripts = append(edited.Scripts, scripts.NewScript("202611020902_more.sql", "ALTER TABLE party ADD COLUMN IF NOT EXISTS phone text"))
	if _, err := r.Apply(ctx, edited); !errors.Is(err, scripts.ErrMigrationEdited) {
		t.Fatalf("expected ErrMigrationEdited, got %v", err)
	}
	var hasPhone bool
	_ = conn.QueryRow(`SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='biz_parties' AND table_name='party' AND column_name='phone')`).Scan(&hasPhone)
	if hasPhone {
		t.Fatal("later scripts must not run after an edited script")
	}

	// Digest ổn định và đổi khi sổ đổi
	d1, _ := r.Digest(ctx, "parties")
	d2, _ := r.Digest(ctx, "parties")
	if d1 == "" || d1 != d2 {
		t.Fatal("digest must be stable")
	}
}

func TestRunnerRefusesLintViolations(t *testing.T) {
	conn := freshDB(t, "aegis_scripts_lint_test")
	r := &scripts.Runner{DB: conn}
	bad := partiesPkg(scripts.NewScript("202611020903_drop.sql", "DROP TABLE party"))
	if _, err := r.Apply(context.Background(), platformPkg(t), bad); err == nil || !strings.Contains(err.Error(), "DB002") {
		t.Fatalf("expected lint failure DB002, got %v", err)
	}
}

func TestEmbeddedPlatformAndMasterPassLint(t *testing.T) {
	master, _ := db.Master("test")
	for _, p := range []scripts.Package{platformPkg(t), master} {
		if vs := scripts.Lint(p); len(vs) > 0 {
			t.Fatalf("%s: %v", p.Name, vs)
		}
	}
}
