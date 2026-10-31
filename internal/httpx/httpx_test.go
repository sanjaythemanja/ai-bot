package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sanjayrj/ai-bot/internal/auth"
	"github.com/sanjayrj/ai-bot/internal/store"
)

var ok = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

func asUser(r *http.Request, id string) *http.Request {
	return r.WithContext(auth.WithUser(r.Context(), store.User{ID: id}))
}

func TestRateLimitPerUser(t *testing.T) {
	h := NewRateLimiter(8, "/api/chat").Wrap(ok) // burst = 2
	codes := func(user string, n int) []int {
		var out []int
		for i := 0; i < n; i++ {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, asUser(httptest.NewRequest("POST", "/api/chat", nil), user))
			out = append(out, rec.Code)
		}
		return out
	}
	alice := codes("alice", 4)
	if alice[0] != 200 || alice[1] != 200 || alice[3] != http.StatusTooManyRequests {
		t.Fatalf("alice: expected burst then 429, got %v", alice)
	}
	if bob := codes("bob", 1); bob[0] != 200 {
		t.Fatalf("one user's limit must not affect another, got %v", bob)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, asUser(httptest.NewRequest("GET", "/api/status", nil), "alice"))
	if rec.Code != 200 {
		t.Fatalf("other routes are not limited, got %d", rec.Code)
	}
}

func TestProtectMetrics(t *testing.T) {
	get := func(h http.Handler, remote, authz string) int {
		r := httptest.NewRequest("GET", "/metrics", nil)
		r.RemoteAddr = remote
		if authz != "" {
			r.Header.Set("Authorization", authz)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	local := ProtectMetrics("", ok)
	if get(local, "127.0.0.1:5000", "") != 200 || get(local, "203.0.113.9:5000", "") != 404 {
		t.Fatal("without a token, /metrics must be local-only")
	}
	tok := ProtectMetrics("s3cret", ok)
	if get(tok, "203.0.113.9:5000", "Bearer s3cret") != 200 || get(tok, "203.0.113.9:5000", "Bearer nope") != 404 {
		t.Fatal("with a token, /metrics needs the right bearer token")
	}
}

func TestSecurityHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	SecurityHeaders(true, ok).ServeHTTP(rec, httptest.NewRequest("GET", "/api/me", nil))
	h := rec.Header()
	if !strings.Contains(h.Get("Content-Security-Policy"), "frame-ancestors 'none'") ||
		h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Strict-Transport-Security") == "" ||
		h.Get("Cache-Control") != "no-store" {
		t.Fatalf("missing headers: %v", h)
	}
	rec = httptest.NewRecorder()
	SecurityHeaders(false, ok).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Header().Get("Strict-Transport-Security") != "" {
		t.Fatal("HSTS must be off on plain HTTP")
	}
}
