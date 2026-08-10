package server

import (
	"net"
	"net/http"
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
	if s.config.TrustProxy {
		if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]); net.ParseIP(forwarded) != nil {
			return forwarded
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && net.ParseIP(host) != nil {
		return host
	}
	if net.ParseIP(r.RemoteAddr) != nil {
		return r.RemoteAddr
	}
	return "unknown"
}
