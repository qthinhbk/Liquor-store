package server

import (
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

type rateLimitEntry struct {
	count   int
	resetAt time.Time
}

type rateLimitStore struct {
	mu         sync.Mutex
	entries    map[string]rateLimitEntry
	maxEntries int
}

func newRateLimitStore(maxEntries int) *rateLimitStore {
	return &rateLimitStore{entries: make(map[string]rateLimitEntry), maxEntries: maxEntries}
}

func (s *rateLimitStore) allow(key string, maximum int, window time.Duration, now time.Time) (bool, time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, exists := s.entries[key]
	if exists && now.Before(entry.resetAt) {
		if entry.count >= maximum {
			return false, time.Until(entry.resetAt)
		}
		entry.count++
		s.entries[key] = entry
		return true, time.Until(entry.resetAt)
	}

	if len(s.entries) >= s.maxEntries {
		for candidate, value := range s.entries {
			if !now.Before(value.resetAt) {
				delete(s.entries, candidate)
			}
		}
		if len(s.entries) >= s.maxEntries {
			return false, window
		}
	}
	s.entries[key] = rateLimitEntry{count: 1, resetAt: now.Add(window)}
	return true, window
}

func (s *Server) authEndpoint(name string, maximum int, window time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.requireTrustedOrigin(w, r) {
			return
		}
		key := name + ":" + s.clientIP(r)
		allowed, retryAfter := s.limits.allow(key, maximum, window, time.Now())
		if !allowed {
			seconds := max(1, int(retryAfter.Round(time.Second).Seconds()))
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			writeError(w, http.StatusTooManyRequests, "Too Many Requests", "Too many authentication attempts. Try again later.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireTrustedOrigin(w http.ResponseWriter, r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" && s.config.Environment != "production" {
		return true
	}
	if origin != s.config.WebOrigin {
		writeError(w, http.StatusForbidden, "Forbidden", "Request origin is not allowed.")
		return false
	}
	return true
}

func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil || peer.Zone() != "" {
		return "unknown"
	}
	peer = peer.Unmap()
	trusted := func(ip netip.Addr) bool {
		for _, prefix := range s.config.TrustedProxyCIDRs {
			if prefix.Contains(ip) {
				return true
			}
		}
		return false
	}
	if !s.config.TrustProxy || !trusted(peer) {
		return peer.String()
	}
	forwarded := strings.Join(r.Header.Values("X-Forwarded-For"), ",")
	if forwarded == "" || len(forwarded) > 4096 {
		return peer.String()
	}
	parts := strings.Split(forwarded, ",")
	if len(parts) > 32 {
		return peer.String()
	}
	chain := make([]netip.Addr, 0, len(parts))
	for _, part := range parts {
		ip, err := netip.ParseAddr(strings.TrimSpace(part))
		if err != nil || ip.Zone() != "" {
			return peer.String()
		}
		chain = append(chain, ip.Unmap())
	}
	for i := len(chain) - 1; i >= 0; i-- {
		if !trusted(chain[i]) {
			return chain[i].String()
		}
	}
	return peer.String()
}
