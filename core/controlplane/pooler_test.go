package controlplane

import (
	"net/url"
	"testing"
)

func newTestStore() *Store {
	return &Store{baseHost: "localhost", basePort: "5432", baseUser: "postgres", basePass: "p@ss", baseSSLMode: "disable"}
}

func TestBuildAdminConnectionStringEscapesCredentials(t *testing.T) {
	got := newTestStore().BuildAdminConnectionString("auth_acme")
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if pw, _ := u.User.Password(); pw != "p@ss" || u.Host != "localhost:5432" || u.Path != "/auth_acme" || u.Query().Get("sslmode") != "disable" {
		t.Fatalf("unexpected admin DSN: %s", got)
	}
}

func TestBuildRuntimeConnectionString(t *testing.T) {
	s := newTestStore()
	admin := s.BuildAdminConnectionString("auth_acme")

	if got := s.BuildRuntimeConnectionString("acme", admin); got != admin {
		t.Fatalf("without pooler DSN must be unchanged, got %s", got)
	}

	s.ConfigureRuntimePooler([]string{"localhost:6432", " localhost:6433 ", ""})
	got := s.BuildRuntimeConnectionString("acme", admin)
	u, _ := url.Parse(got)
	if u.Host != "localhost:6432" && u.Host != "localhost:6433" {
		t.Fatalf("expected a pgbouncer node, got %s", u.Host)
	}
	if u.Query().Get("binary_parameters") != "yes" || u.Query().Get("sslmode") != "disable" || u.Path != "/auth_acme" {
		t.Fatalf("unexpected runtime DSN: %s", got)
	}
	if again := s.BuildRuntimeConnectionString("acme", admin); again != got {
		t.Fatal("tenant must stick to the same node")
	}

	// Phân tải: nhiều tenant phải rơi vào cả 2 node
	seen := map[string]bool{}
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		u, _ := url.Parse(s.BuildRuntimeConnectionString(id, s.BuildAdminConnectionString("auth_"+id)))
		seen[u.Host] = true
	}
	if len(seen) != 2 {
		t.Fatalf("tenants should spread across nodes, got %v", seen)
	}

	custom := "postgres://u:p@other-db:5432/x?sslmode=disable"
	if got := s.BuildRuntimeConnectionString("acme", custom); got != custom {
		t.Fatalf("custom DSN to another server must be unchanged, got %s", got)
	}
}
