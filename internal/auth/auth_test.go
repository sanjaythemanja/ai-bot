package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/sanjayrj/ai-bot/internal/secure"
	"github.com/sanjayrj/ai-bot/internal/store"
)

func TestSessionLifecycle(t *testing.T) {
	users := store.NewMemory()
	u, _ := users.UpsertUser(context.Background(), "g:1", "me@example.com", "Me")
	s := &Sessions{Users: users, TTL: time.Hour}

	protected := s.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, err := UserFrom(r.Context())
		if err != nil || got.ID != u.ID {
			t.Errorf("handler saw user %+v err %v", got, err)
		}
		w.WriteHeader(http.StatusOK)
	}))

	// No cookie: API refused, UI files allowed.
	rec := httptest.NewRecorder()
	protected.ServeHTTP(rec, httptest.NewRequest("GET", "/api/status", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without session, got %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	s.Require(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })).
		ServeHTTP(rec, httptest.NewRequest("GET", "/assets/app.js", nil))
	if rec.Code != 200 {
		t.Fatalf("static files must stay public, got %d", rec.Code)
	}

	// Start a session; the cookie unlocks the API.
	rec = httptest.NewRecorder()
	if err := s.Start(rec, httptest.NewRequest("GET", "/auth/google/callback", nil), u.ID); err != nil {
		t.Fatal(err)
	}
	cookie := rec.Result().Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie flags wrong: %+v", cookie)
	}
	req := httptest.NewRequest("GET", "/api/status", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	protected.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("expected 200 with session, got %d", rec.Code)
	}

	// Logout ends it server-side: the same cookie no longer works.
	out := httptest.NewRequest("POST", "/auth/logout", nil)
	out.AddCookie(cookie)
	s.End(httptest.NewRecorder(), out)
	req = httptest.NewRequest("GET", "/api/status", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	protected.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 after logout, got %d", rec.Code)
	}

	// A forged cookie is refused.
	req = httptest.NewRequest("GET", "/api/status", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: "forged"})
	rec = httptest.NewRecorder()
	protected.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for forged cookie, got %d", rec.Code)
	}
}

func TestAllowlist(t *testing.T) {
	a := ParseAllowlist(" Boss@Example.com, @team.io ,")
	for email, want := range map[string]bool{
		"boss@example.com": true, "BOSS@example.com": true, "dev@team.io": true,
		"other@example.com": false, "x@evilteam.io": false,
	} {
		if a.Allows(email) != want {
			t.Errorf("Allows(%q) = %v, want %v", email, !want, want)
		}
	}
	if !ParseAllowlist("").Allows("anyone@x.com") {
		t.Error("empty allowlist should allow everyone")
	}
}

func TestVaultEncryptsTokens(t *testing.T) {
	users := store.NewMemory()
	u, _ := users.UpsertUser(context.Background(), "g:2", "v@example.com", "V")
	box, err := secure.NewBox("0123456789abcdef0123456789abcdef-test")
	if err != nil {
		t.Fatal(err)
	}
	v := Vault{Users: users, Box: box}
	if err := v.Save(context.Background(), u.ID, &oauth2.Token{AccessToken: "at-secret", RefreshToken: "rt-secret"}); err != nil {
		t.Fatal(err)
	}
	sealed, _ := users.GoogleToken(context.Background(), u.ID)
	if string(sealed) == "" || containsBytes(sealed, "rt-secret") {
		t.Fatal("token stored in plaintext")
	}
	got, err := v.Load(context.Background(), u.ID)
	if err != nil || got.RefreshToken != "rt-secret" {
		t.Fatalf("vault round trip: %+v %v", got, err)
	}
	other, _ := secure.NewBox("a-completely-different-secret-value-xyz")
	if _, err := (Vault{Users: users, Box: other}).Load(context.Background(), u.ID); err == nil {
		t.Fatal("token decrypted with the wrong APP_SECRET")
	}
	if _, err := secure.NewBox("short"); err == nil {
		t.Fatal("short APP_SECRET accepted")
	}
}

func containsBytes(b []byte, s string) bool {
	for i := 0; i+len(s) <= len(b); i++ {
		if string(b[i:i+len(s)]) == s {
			return true
		}
	}
	return false
}
