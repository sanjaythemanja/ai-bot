// Package httpx holds small HTTP middlewares for production hardening:
// security headers, per-user rate limits, and protecting /metrics.
package httpx

import (
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/sanjayrj/ai-bot/internal/auth"
)

// csp allows only the app's own scripts, styles, fonts and API. Inline
// style attributes are needed by React's style props; no inline scripts.
const csp = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; font-src 'self'; connect-src 'self'; " +
	"frame-ancestors 'none'; base-uri 'self'; form-action 'self'; object-src 'none'"

// SecurityHeaders sets defensive headers on every response. hsts should be
// true only when served over HTTPS.
func SecurityHeaders(hsts bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(self), geolocation=(), payment=()")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		if hsts {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/auth/") {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

// RateLimiter caps expensive requests per signed-in user.
type RateLimiter struct {
	perMinute int
	paths     map[string]bool

	mu    sync.Mutex
	users map[string]*entry
}

type entry struct {
	lim  *rate.Limiter
	seen time.Time
}

// NewRateLimiter allows perMinute requests per user (with a small burst) to
// the given exact paths, e.g. "/api/chat".
func NewRateLimiter(perMinute int, paths ...string) *RateLimiter {
	rl := &RateLimiter{perMinute: perMinute, paths: map[string]bool{}, users: map[string]*entry{}}
	for _, p := range paths {
		rl.paths[p] = true
	}
	return rl
}

func (rl *RateLimiter) limiter(userID string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	e, ok := rl.users[userID]
	if !ok {
		burst := max(2, rl.perMinute/4)
		e = &entry{lim: rate.NewLimiter(rate.Limit(float64(rl.perMinute)/60), burst)}
		rl.users[userID] = e
	}
	e.seen = now
	// Forget idle users so the map doesn't grow forever.
	if len(rl.users) > 1000 {
		for id, x := range rl.users {
			if now.Sub(x.seen) > time.Hour {
				delete(rl.users, id)
			}
		}
	}
	return e.lim
}

// Wrap must run inside auth.Sessions.Require so the user is known.
func (rl *RateLimiter) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && rl.paths[r.URL.Path] {
			if u, err := auth.UserFrom(r.Context()); err == nil && !rl.limiter(u.ID).Allow() {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "20")
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "You're sending requests quickly. Please wait a few seconds and try again."})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// ProtectMetrics allows /metrics with "Authorization: Bearer <token>", or,
// when no token is configured, only from the same machine.
func ProtectMetrics(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token != "" {
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) == 1 {
				next.ServeHTTP(w, r)
				return
			}
		} else if isLoopback(r.RemoteAddr) && r.Header.Get("X-Forwarded-For") == "" {
			next.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	})
}

func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
