package scripts

import (
	"slices"
	"testing"
)

func codes(vs []Violation) []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, v.Code)
	}
	return out
}

func lintModule(name, sql string, local ...string) []string {
	return codes(Lint(Package{Name: "parties", Schema: "biz_parties", Profile: ProfileModule, LocalTables: local, Scripts: []Script{NewScript(name, sql)}}))
}

const goodTable = `CREATE TABLE IF NOT EXISTS party (
    id uuid NOT NULL,
    store_id uuid NOT NULL DEFAULT platform.store_id(),
    name text NOT NULL, -- DROP TABLE trong chú thích không tính
    note text NOT NULL DEFAULT 'drop table x; delete from y',
    PRIMARY KEY (store_id, id),
    UNIQUE (store_id, name)
);
CREATE UNIQUE INDEX IF NOT EXISTS party_name ON party (store_id, lower(name));`

func TestLintAcceptsGoodModuleScript(t *testing.T) {
	if got := lintModule("202611020900_party.sql", goodTable); len(got) != 0 {
		t.Fatalf("expected no violations, got %v", got)
	}
}

func TestLintRules(t *testing.T) {
	cases := []struct {
		name, file, sql, want string
		local                 []string
	}{
		{"bad file name", "party.sql", goodTable, "DB001", nil},
		{"drop", "202611020900_x.sql", "DROP TABLE party", "DB002", nil},
		{"rename", "202611020900_x.sql", "ALTER TABLE party RENAME COLUMN name TO full_name", "DB002", nil},
		{"alter type", "202611020900_x.sql", "ALTER TABLE party ALTER COLUMN name TYPE varchar(10)", "DB002", nil},
		{"delete", "202611020900_x.sql", "DELETE FROM party", "DB002", nil},
		{"grant", "202611020900_x.sql", "GRANT SELECT ON party TO public", "DB003", nil},
		{"function", "202611020900_x.sql", "CREATE FUNCTION f() RETURNS int LANGUAGE sql AS $$ SELECT 1 $$", "DB003", nil},
		{"set", "202611020900_x.sql", "SET search_path TO biz_sales", "DB003", nil},
		{"qualified table", "202611020900_x.sql", "ALTER TABLE biz_sales.orders ADD COLUMN x text", "DB004", nil},
		{"cross-schema fk", "202611020900_x.sql", "ALTER TABLE party ADD COLUMN order_id uuid REFERENCES biz_sales.orders(id)", "DB004", nil},
		{"no store_id", "202611020900_x.sql", "CREATE TABLE t (id uuid PRIMARY KEY)", "DB005", nil},
		{"unique without store_id", "202611020900_x.sql", "CREATE TABLE t (id uuid, store_id uuid NOT NULL DEFAULT platform.store_id(), code text, PRIMARY KEY (store_id, id), UNIQUE (code))", "DB006", nil},
		{"unique index without store_id", "202611020900_x.sql", "CREATE UNIQUE INDEX IF NOT EXISTS t_code ON t (code)", "DB006", nil},
		{"dml on synced", "202611020900_x.sql", "INSERT INTO party (id) VALUES ('x')", "DB007", nil},
		{"concurrently without marker", "202611020900_x.sql", "CREATE INDEX CONCURRENTLY IF NOT EXISTS i ON party (name)", "DB008", nil},
		{"marker with two statements", "202611020900_x.sql", "-- platform:concurrently\nCREATE INDEX CONCURRENTLY IF NOT EXISTS i ON party (name);\nCREATE INDEX j ON party (id);", "DB008", nil},
		{"not null without default", "202611020900_x.sql", "ALTER TABLE party ADD COLUMN code text NOT NULL", "DB009", nil},
		{"bad timeout", "202611020900_x.sql", "-- platform:timeout 99m\nALTER TABLE party ADD COLUMN code text", "DB011", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := lintModule(c.file, c.sql, c.local...); !slices.Contains(got, c.want) {
				t.Fatalf("expected %s, got %v", c.want, got)
			}
		})
	}
}

func TestLintAllowsLocalTablesAndMarkers(t *testing.T) {
	sql := "CREATE TABLE IF NOT EXISTS print_queue (id bigserial PRIMARY KEY, body text);\nINSERT INTO print_queue (body) VALUES ('hello');"
	if got := lintModule("202611020900_local.sql", sql, "print_queue"); len(got) != 0 {
		t.Fatalf("local table must allow DML and skip store_id, got %v", got)
	}
	conc := "-- platform:concurrently\nCREATE INDEX CONCURRENTLY IF NOT EXISTS party_note ON party (note)"
	s := NewScript("202611020901_idx.sql", conc)
	if !s.Concurrently || len(lintModule(s.Name, conc)) != 0 {
		t.Fatalf("valid concurrently script rejected: %v", lintModule(s.Name, conc))
	}
	if !NewScript("202611020902_x.sql", "-- platform:allow DB010 old binary writes null\nALTER TABLE party ADD COLUMN x text").RollbackUnsafe {
		t.Fatal("DB010 marker not parsed")
	}
}

func TestChecksumIgnoresLineEndings(t *testing.T) {
	if NewScript("a.sql", "SELECT 1;\r\nSELECT 2;").Checksum != NewScript("a.sql", "SELECT 1;\nSELECT 2;").Checksum {
		t.Fatal("CRLF must not change checksum")
	}
}
