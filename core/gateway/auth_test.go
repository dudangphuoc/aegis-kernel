package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dudangphuoc/aegis-kernel/core/contract"
	"github.com/dudangphuoc/aegis-kernel/sdk/security"
)

var testSecret = []byte("gateway-test-secret-gateway-test-secret")

func signFor(t *testing.T, tenantID string, ttl time.Duration) string {
	t.Helper()
	now := time.Now()
	tok, err := security.SignToken(testSecret, security.TokenClaims{
		Subject: "USR-1", Username: "admin", TenantID: tenantID, Role: "TENANT_ADMIN",
		SecurityLevel: security.SecurityLevelStandard, IssuedAt: now.Unix(), ExpiresAt: now.Add(ttl).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestVerifyRequestJWT(t *testing.T) {
	gw := &APIGateway{jwtSecret: testSecret}
	cases := map[string]struct {
		auth       string
		wantStatus int
		wantClaims bool
	}{
		"no token":     {"", 0, false},
		"valid":        {"Bearer " + signFor(t, "acme", time.Hour), 0, true},
		"other tenant": {"Bearer " + signFor(t, "globex", time.Hour), http.StatusForbidden, false},
		"expired":      {"Bearer " + signFor(t, "acme", -time.Minute), http.StatusUnauthorized, false},
		"wrong scheme": {"Basic abc", http.StatusUnauthorized, false},
		"garbage":      {"Bearer not-a-jwt", http.StatusUnauthorized, false},
	}
	for name, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		if c.auth != "" {
			req.Header.Set("Authorization", c.auth)
		}
		claims, status, _ := gw.verifyRequestJWT(req, "acme")
		if status != c.wantStatus || (claims != nil) != c.wantClaims {
			t.Errorf("%s: status=%d claims=%v", name, status, claims != nil)
		}
	}
}

func TestBuildModuleHeadersStripsSpoofedActor(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.RemoteAddr = "10.0.0.7:5555"
	req.Header.Set("X-Actor-Id", "spoofed")
	req.Header.Set("X-Actor-Role", "SUPER_ADMIN")
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("User-Agent", "curl/8")
	req.Header.Set("X-Custom", "keep")

	h := buildModuleHeaders(req, nil)
	if _, ok := h[contract.HeaderActorID]; ok {
		t.Fatal("spoofed actor header must be removed for anonymous request")
	}
	if _, ok := h["Authorization"]; ok {
		t.Fatal("raw token must not be forwarded to modules")
	}
	if h[contract.HeaderClientIP] != "10.0.0.7" || h[contract.HeaderUserAgent] != "curl/8" || h["X-Custom"] != "keep" {
		t.Fatalf("unexpected headers: %v", h)
	}

	h = buildModuleHeaders(req, &security.TokenClaims{Subject: "USR-1", Username: "admin", Role: "TENANT_ADMIN"})
	if h[contract.HeaderActorID] != "USR-1" || h[contract.HeaderActorRole] != "TENANT_ADMIN" {
		t.Fatalf("actor headers not set from claims: %v", h)
	}
}

func TestMaskDSN(t *testing.T) {
	got := maskDSN("postgres://postgres:s3cret@localhost:5432/auth_acme?sslmode=disable")
	if got != "postgres://postgres:***@localhost:5432/auth_acme?sslmode=disable" {
		t.Fatalf("got %s", got)
	}
	if maskDSN("host=x password=y") != "***" {
		t.Fatal("non-URL DSN must be fully masked")
	}
}

func TestNextCallChain(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	if c, err := nextCallChain(req, "parties"); err != nil || c != "parties" {
		t.Fatalf("empty chain: %q %v", c, err)
	}
	req.Header.Set(contract.HeaderCallChain, "auth, parties")
	if _, err := nextCallChain(req, "auth"); err == nil {
		t.Fatal("auth -> parties -> auth must be blocked")
	}
	if c, err := nextCallChain(req, "audit"); err != nil || c != "auth,parties,audit" {
		t.Fatalf("unexpected chain: %q %v", c, err)
	}
	req.Header.Set(contract.HeaderCallChain, "m1,m2,m3,m4,m5,m6,m7,m8")
	if _, err := nextCallChain(req, "m9"); err == nil {
		t.Fatal("chain deeper than MaxCallChainHops must be blocked")
	}
}
