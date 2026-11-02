package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolveClientIP(t *testing.T) {
	trusted, err := ParseTrustedProxies("10.0.0.0/8, 127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	gw := &APIGateway{trustedProxies: trusted}

	cases := map[string]struct {
		remote, xff, xRealIP string
		want                 string
	}{
		"untrusted peer spoofing XFF":   {"203.0.113.9:5000", "1.2.3.4", "", "203.0.113.9:5000"},
		"untrusted peer spoofing XRIP":  {"203.0.113.9:5000", "", "1.2.3.4", "203.0.113.9:5000"},
		"trusted proxy, single hop":     {"10.0.0.5:5000", "198.51.100.7", "", "198.51.100.7"},
		"client prepends fake hop":      {"10.0.0.5:5000", "6.6.6.6, 198.51.100.7", "", "198.51.100.7"},
		"chain of trusted proxies":      {"127.0.0.1:5000", "198.51.100.7, 10.1.1.1", "", "198.51.100.7"},
		"all hops trusted -> leftmost":  {"10.0.0.5:5000", "10.2.2.2, 10.1.1.1", "", "10.2.2.2"},
		"trusted proxy uses X-Real-IP":  {"10.0.0.5:5000", "", "198.51.100.8", "198.51.100.8"},
		"malformed XFF keeps peer":      {"10.0.0.5:5000", "not-an-ip", "", "10.0.0.5:5000"},
		"trusted proxy without headers": {"10.0.0.5:5000", "", "", "10.0.0.5:5000"},
	}
	for name, c := range cases {
		var got string
		h := gw.clientIPMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = r.RemoteAddr }))
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = c.remote
		if c.xff != "" {
			req.Header.Set("X-Forwarded-For", c.xff)
		}
		if c.xRealIP != "" {
			req.Header.Set("X-Real-IP", c.xRealIP)
		}
		h.ServeHTTP(httptest.NewRecorder(), req)
		if got != c.want {
			t.Errorf("%s: got %s want %s", name, got, c.want)
		}
	}

	if _, err := ParseTrustedProxies("10.0.0.0/8,bogus"); err == nil {
		t.Fatal("invalid entry must be rejected")
	}
}
