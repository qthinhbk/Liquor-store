package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/liquor-store/security-api/internal/config"
)

func TestTerminalAlertDecisionPermissions(t *testing.T) {
	for _, tc := range []struct {
		from, to, role string
		want           int
	}{
		{"NEW", "ACKNOWLEDGED", "OPERATOR", 0}, {"NEW", "DISMISSED", "OPERATOR", 0},
		{"NEW", "RESOLVED", "OPERATOR", 403}, {"ACKNOWLEDGED", "RESOLVED", "MANAGER", 0},
		{"RESOLVED", "DISMISSED", "OPERATOR", 403}, {"DISMISSED", "RESOLVED", "OPERATOR", 403},
		{"RESOLVED", "DISMISSED", "MANAGER", 0}, {"RESOLVED", "DISMISSED", "OWNER", 0},
		{"DISMISSED", "RESOLVED", "OWNER", 0}, {"RESOLVED", "ACKNOWLEDGED", "OWNER", 409},
		{"RESOLVED", "RESOLVED", "OWNER", 409}, {"NEW", "DISMISSED", "", 403},
	} {
		if got := alertTransitionStatus(tc.from, tc.to, tc.role); got != tc.want {
			t.Errorf("%+v: got %d", tc, got)
		}
	}
}

func TestProxyChainClientIdentity(t *testing.T) {
	s := &Server{config: config.Config{TrustProxy: true, TrustedProxyCIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")}}}
	for _, tc := range []struct{ peer, xff, want string }{
		{"10.0.0.5:1234", "198.51.100.1, 203.0.113.10", "203.0.113.10"},
		{"10.0.0.5:1234", "203.0.113.10, 10.0.0.4", "203.0.113.10"},
		{"192.0.2.8:1234", "198.51.100.1", "192.0.2.8"},
		{"10.0.0.5:1234", "invalid, 203.0.113.10", "10.0.0.5"},
		{"[::ffff:192.0.2.8]:1234", "198.51.100.1", "192.0.2.8"},
		{"10.0.0.5:1234", "::ffff:203.0.113.10", "203.0.113.10"},
		{"10.0.0.5:1234", "10.0.0.4", "10.0.0.5"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = tc.peer
		r.Header.Set("X-Forwarded-For", tc.xff)
		if got := s.clientIP(r); got != tc.want {
			t.Errorf("%+v got %s", tc, got)
		}
	}
	s.config.TrustedProxyCIDRs = nil
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.5:1234"
	r.Header.Set("X-Forwarded-For", "198.51.100.1")
	if got := s.clientIP(r); got != "10.0.0.5" {
		t.Fatal("unconfigured proxy must not trust forwarding headers")
	}
}

func TestForgedForwardedIPsCannotBypassAuthLimit(t *testing.T) {
	s := &Server{config: config.Config{TrustProxy: true, TrustedProxyCIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")}}, limits: newRateLimitStore(10000)}
	handler := s.authEndpoint("login", 10, time.Minute, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	accepted := 0
	for i := 1; i <= 100; i++ {
		r := httptest.NewRequest("POST", "/", nil)
		r.RemoteAddr = "10.0.0.5:1234"
		r.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d, 203.0.113.10", i))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code == 204 {
			accepted++
		}
	}
	if accepted != 10 || len(s.limits.entries) != 1 {
		t.Fatalf("accepted=%d buckets=%d", accepted, len(s.limits.entries))
	}
}
