// Package auth handles sign-in sessions and the signed-in user for each
// request. Sessions are random tokens in an HttpOnly cookie; only their
// SHA-256 is stored, so a database leak can't be replayed as a login.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/sanjayrj/ai-bot/internal/secure"
	"github.com/sanjayrj/ai-bot/internal/store"
)

// ErrUnauthorized means the request has no signed-in user.
var ErrUnauthorized = errors.New("not signed in")

type ctxKey struct{}

// WithUser returns ctx carrying the signed-in user.
func WithUser(ctx context.Context, u store.User) context.Context {
	return context.WithValue(ctx, ctxKey{}, u)
}

// UserFrom returns the signed-in user, or ErrUnauthorized.
func UserFrom(ctx context.Context) (store.User, error) {
	if u, ok := ctx.Value(ctxKey{}).(store.User); ok && u.ID != "" {
		return u, nil
	}
	return store.User{}, ErrUnauthorized
}

// CookieName is the session cookie.
const CookieName = "ea_session"

// Sessions issues and checks session cookies.
type Sessions struct {
	Users  store.Users
	Secure bool          // set the Secure flag (HTTPS deployments)
	TTL    time.Duration // session lifetime

	// Optional: let one bearer token act as one user on /api/tools, for
	// external agents. Both must be set.
	AgentToken string
	AgentEmail string

	// Optional: sign every request in as this user (demo mode only).
	DemoUser *store.User
}

func hashToken(tok string) []byte {
	h := sha256.Sum256([]byte(tok))
	return h[:]
}

// Start creates a session for userID and sets the cookie.
func (s *Sessions) Start(w http.ResponseWriter, r *http.Request, userID string) error {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	tok := base64.RawURLEncoding.EncodeToString(b)
	exp := time.Now().Add(s.TTL)
	if err := s.Users.CreateSession(r.Context(), userID, hashToken(tok), exp); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: tok, Path: "/", Expires: exp,
		HttpOnly: true, Secure: s.Secure, SameSite: http.SameSiteLaxMode,
	})
	return nil
}

// End deletes the session server-side and clears the cookie.
func (s *Sessions) End(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(CookieName); err == nil && c.Value != "" {
		_ = s.Users.DeleteSession(r.Context(), hashToken(c.Value))
	}
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: s.Secure, SameSite: http.SameSiteLaxMode,
	})
}

// user resolves the request's user from the cookie (or agent token).
func (s *Sessions) user(r *http.Request) (store.User, error) {
	if s.DemoUser != nil {
		return *s.DemoUser, nil
	}
	if s.AgentToken != "" && s.AgentEmail != "" && strings.HasPrefix(r.URL.Path, "/api/tools") {
		got := []byte(r.Header.Get("Authorization"))
		if subtle.ConstantTimeCompare(got, []byte("Bearer "+s.AgentToken)) == 1 {
			return s.Users.UserByEmail(r.Context(), s.AgentEmail)
		}
	}
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return store.User{}, ErrUnauthorized
	}
	return s.Users.SessionUser(r.Context(), hashToken(c.Value))
}

// Require rejects /api/* requests that have no signed-in user and puts the
// user in the request context. Everything else (UI files, /auth/*) passes.
func (s *Sessions) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		u, err := s.user(r)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Please sign in."})
			return
		}
		next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), u)))
	})
}

// Allowlist says who may sign in: exact emails or "@domain" entries.
// An empty list allows anyone Google lets through (e.g. OAuth test users).
type Allowlist []string

// ParseAllowlist splits a comma-separated ALLOWED_EMAILS value.
func ParseAllowlist(v string) Allowlist {
	var out Allowlist
	for _, p := range strings.Split(v, ",") {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (a Allowlist) Allows(email string) bool {
	if len(a) == 0 {
		return true
	}
	email = strings.ToLower(email)
	for _, p := range a {
		if p == email || (strings.HasPrefix(p, "@") && strings.HasSuffix(email, p)) {
			return true
		}
	}
	return false
}

// Vault stores each user's Google OAuth token encrypted at rest.
type Vault struct {
	Users store.Users
	Box   *secure.Box
}

// Load returns the user's decrypted token.
func (v Vault) Load(ctx context.Context, userID string) (*oauth2.Token, error) {
	sealed, err := v.Users.GoogleToken(ctx, userID)
	if err != nil {
		return nil, err
	}
	raw, err := v.Box.Open(sealed)
	if err != nil {
		return nil, err
	}
	var t oauth2.Token
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// Save encrypts and stores the user's token.
func (v Vault) Save(ctx context.Context, userID string, t *oauth2.Token) error {
	raw, err := json.Marshal(t)
	if err != nil {
		return err
	}
	sealed, err := v.Box.Seal(raw)
	if err != nil {
		return err
	}
	return v.Users.SaveGoogleToken(ctx, userID, sealed)
}
