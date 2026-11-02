package multitenant

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dudangphuoc/aegis-kernel/core/contract"
)

type fakeDB struct {
	dsn    string
	closed atomic.Bool
}

func (f *fakeDB) Close() error { f.closed.Store(true); return nil }

func tenantCtx(id, dsn string) context.Context {
	return WithTenant(context.Background(), contract.TenantRequestContext{TenantID: id, ConnectionString: dsn})
}

func newTestManager(t *testing.T, opts ...Option) (*ConnectionManager[*fakeDB], *time.Time) {
	t.Helper()
	opts = append([]Option{WithJanitorInterval(0)}, opts...)
	m := NewConnectionManager[*fakeDB](func(_, dsn string) (*fakeDB, error) {
		return &fakeDB{dsn: dsn}, nil
	}, opts...)
	now := time.Unix(0, 0)
	m.now = func() time.Time { return now }
	t.Cleanup(func() { _ = m.CloseAll() })
	return m, &now
}

func TestResolveReusesPoolPerTenant(t *testing.T) {
	m, _ := newTestManager(t)
	a1, _, _ := m.Resolve(tenantCtx("a", "dsn-a"))
	a2, _, _ := m.Resolve(tenantCtx("a", "dsn-a"))
	b, _, _ := m.Resolve(tenantCtx("b", "dsn-b"))
	if a1 != a2 {
		t.Fatal("expected same pool for same tenant")
	}
	if a1 == b {
		t.Fatal("expected isolated pools per tenant")
	}
}

func TestSafeRebindClosesOldPool(t *testing.T) {
	m, _ := newTestManager(t)
	old, _, _ := m.Resolve(tenantCtx("a", "dsn-1"))
	fresh, _, _ := m.Resolve(tenantCtx("a", "dsn-2"))
	if !old.closed.Load() {
		t.Fatal("old pool should be closed after rebind")
	}
	if fresh.dsn != "dsn-2" || m.Len() != 1 {
		t.Fatalf("expected single rebound pool, got dsn=%s len=%d", fresh.dsn, m.Len())
	}
}

func TestIdleTTLEviction(t *testing.T) {
	m, now := newTestManager(t, WithIdleTTL(15*time.Minute))
	a, _, _ := m.Resolve(tenantCtx("a", "dsn-a"))
	*now = now.Add(10 * time.Minute)
	b, _, _ := m.Resolve(tenantCtx("b", "dsn-b"))
	*now = now.Add(6 * time.Minute)

	m.evictIdle()
	if !a.closed.Load() || b.closed.Load() || m.Len() != 1 {
		t.Fatalf("expected only idle tenant a evicted (a=%v b=%v len=%d)", a.closed.Load(), b.closed.Load(), m.Len())
	}
}

func TestLRUEviction(t *testing.T) {
	m, now := newTestManager(t, WithMaxTenants(2))
	a, _, _ := m.Resolve(tenantCtx("a", "dsn-a"))
	*now = now.Add(time.Second)
	b, _, _ := m.Resolve(tenantCtx("b", "dsn-b"))
	*now = now.Add(time.Second)
	_, _, _ = m.Resolve(tenantCtx("a", "dsn-a")) // a vừa được dùng -> b là LRU
	*now = now.Add(time.Second)
	_, _, _ = m.Resolve(tenantCtx("c", "dsn-c"))

	if a.closed.Load() || !b.closed.Load() || m.Len() != 2 {
		t.Fatalf("expected b evicted as LRU (a=%v b=%v len=%d)", a.closed.Load(), b.closed.Load(), m.Len())
	}
}

func TestCloseAll(t *testing.T) {
	m := NewConnectionManager[*fakeDB](func(_, dsn string) (*fakeDB, error) { return &fakeDB{dsn: dsn}, nil })
	a, _, _ := m.Resolve(tenantCtx("a", "dsn-a"))
	if err := m.CloseAll(); err != nil {
		t.Fatal(err)
	}
	if !a.closed.Load() {
		t.Fatal("pool should be closed")
	}
	if _, _, err := m.Resolve(tenantCtx("a", "dsn-a")); !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("expected ErrManagerClosed, got %v", err)
	}
	if err := m.CloseAll(); err != nil {
		t.Fatal("CloseAll should be idempotent")
	}
}

func TestPeekDoesNotExtendIdleLifetime(t *testing.T) {
	m, now := newTestManager(t, WithIdleTTL(15*time.Minute))
	a, _, _ := m.Resolve(tenantCtx("a", "dsn-a"))
	*now = now.Add(10 * time.Minute)
	if got, ok := m.Peek("a", "dsn-a"); !ok || got != a {
		t.Fatal("peek must return the open pool")
	}
	if _, ok := m.Peek("a", "other-dsn"); ok {
		t.Fatal("peek must not return a pool for a different DSN")
	}
	*now = now.Add(6 * time.Minute)
	m.evictIdle()
	if !a.closed.Load() {
		t.Fatal("peek must not keep an idle tenant's pool alive")
	}
	if _, ok := m.Peek("a", "dsn-a"); ok {
		t.Fatal("evicted pool must not be returned")
	}
}
