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

// Measured Render ingress: the process is reached over loopback, and the request
// arrives with Render's own private hop appended last. Both are non-routable, so
// a remote client can never present either as the peer or as the final hop.
func renderIngressPrefixes() []netip.Prefix {
	return []netip.Prefix{
		netip.MustParsePrefix("::1/128"), netip.MustParsePrefix("127.0.0.1/32"),
		netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("172.16.0.0/12"),
		netip.MustParsePrefix("192.168.0.0/16"), netip.MustParsePrefix("fc00::/7"),
	}
}

func TestLoopbackIngressResolvesConnectingClient(t *testing.T) {
	s := &Server{config: config.Config{TrustProxy: true, TrustedProxyCIDRs: renderIngressPrefixes()}}
	for _, tc := range []struct{ name, peer, xff, want string }{
		{"observed chain: client then Render hop", "[::1]:443", "203.0.113.10, 10.30.141.2", "203.0.113.10"},
		{"forged entry ignored, ingress hop wins", "[::1]:443", "1.2.3.4, 203.0.113.10", "203.0.113.10"},
		{"forged private entry cannot hide the real hop", "[::1]:443", "1.2.3.4, 10.9.9.9, 203.0.113.10, 10.30.141.2", "203.0.113.10"},
		{"single ingress hop", "[::1]:443", "203.0.113.10", "203.0.113.10"},
		{"ipv6 client", "[::1]:443", "2401:d800::1, 10.30.141.2", "2401:d800::1"},
		{"ipv4 loopback peer", "127.0.0.1:443", "203.0.113.10", "203.0.113.10"},
		{"no forwarded header falls back to peer", "[::1]:443", "", "::1"},
		{"malformed chain falls back to peer", "[::1]:443", "not-an-ip, 203.0.113.10", "::1"},
		{"chain of only infrastructure falls back to peer", "[::1]:443", "::1, 10.30.141.2", "::1"},
		{"non-loopback public peer is never trusted", "203.0.113.99:443", "1.2.3.4", "203.0.113.99"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = tc.peer
		if tc.xff != "" {
			r.Header.Set("X-Forwarded-For", tc.xff)
		}
		if got := s.clientIP(r); got != tc.want {
			t.Errorf("%s: got %s want %s", tc.name, got, tc.want)
		}
	}
}

// Without the fix every request shares one bucket; with it, distinct ingress
// addresses are limited independently.
func TestLoopbackIngressSeparatesRateLimitBuckets(t *testing.T) {
	// The chain observed in production: the connecting hop, then Render's own
	// private address appended last.
	request := func(s *Server, client string) string {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "[::1]:443"
		r.Header.Set("X-Forwarded-For", client+", 10.30.141.2")
		return s.clientIP(r)
	}

	shared := &Server{config: config.Config{TrustProxy: false, TrustedProxyCIDRs: renderIngressPrefixes()}}
	if request(shared, "203.0.113.10") != request(shared, "198.51.100.7") {
		t.Fatal("expected the unpatched configuration to collapse clients onto one key")
	}

	// Trusting loopback alone only advances to Render's private hop, which is
	// still shared by every client. That was the measured intermediate state.
	loopbackOnly := &Server{config: config.Config{TrustProxy: true, TrustedProxyCIDRs: []netip.Prefix{
		netip.MustParsePrefix("::1/128"), netip.MustParsePrefix("127.0.0.1/32"),
	}}}
	if request(loopbackOnly, "203.0.113.10") != request(loopbackOnly, "198.51.100.7") {
		t.Fatal("trusting only loopback should still collapse clients onto Render's private hop")
	}

	split := &Server{config: config.Config{TrustProxy: true, TrustedProxyCIDRs: renderIngressPrefixes()}}
	if request(split, "203.0.113.10") == request(split, "198.51.100.7") {
		t.Fatal("full ingress trust must separate distinct clients into different buckets")
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
