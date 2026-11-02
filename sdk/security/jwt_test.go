package security

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var testSecret = []byte("0123456789abcdef0123456789abcdef")

func TestSignAndVerify(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	claims := TokenClaims{TokenID: "j1", Subject: "USR-1", Username: "admin", TenantID: "acme", Role: "TENANT_ADMIN",
		SecurityLevel: SecurityLevelMFA, IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Hour).Unix()}
	tok, err := SignToken(testSecret, claims)
	if err != nil {
		t.Fatal(err)
	}
	got, err := VerifyToken(testSecret, tok, now)
	if err != nil {
		t.Fatal(err)
	}
	if *got != claims {
		t.Fatalf("claims mismatch: %+v", got)
	}
}

func TestVerifyRejects(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	tok, _ := SignToken(testSecret, TokenClaims{TenantID: "acme", ExpiresAt: now.Add(time.Hour).Unix()})
	parts := strings.Split(tok, ".")

	forged, _ := SignToken([]byte("another-secret-another-secret-xx"), TokenClaims{TenantID: "acme", ExpiresAt: now.Add(time.Hour).Unix()})
	tamperedPayload, _ := SignToken(testSecret, TokenClaims{TenantID: "evil", ExpiresAt: now.Add(time.Hour).Unix()})
	noneAlg := "eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0." + parts[1] + "."

	cases := map[string]struct {
		token string
		now   time.Time
		want  error
	}{
		"wrong secret":    {forged, now, ErrInvalidSignature},
		"swapped payload": {parts[0] + "." + strings.Split(tamperedPayload, ".")[1] + "." + parts[2], now, ErrInvalidSignature},
		"alg none":        {noneAlg, now, ErrMalformedToken},
		"expired":         {tok, now.Add(2 * time.Hour), ErrTokenExpired},
		"garbage":         {"abc", now, ErrMalformedToken},
	}
	for name, c := range cases {
		if _, err := VerifyToken(testSecret, c.token, c.now); !errors.Is(err, c.want) {
			t.Errorf("%s: want %v, got %v", name, c.want, err)
		}
	}
}
