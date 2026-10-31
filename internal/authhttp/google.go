// Package authhttp serves the browser-facing sign-in flow: Google OAuth
// redirect, callback, logout, and "who am I". It is plain net/http rather
// than go-kit because it is redirects and cookies, not an API.
package authhttp

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-kit/log"

	"github.com/sanjayrj/ai-bot/internal/auth"
	"github.com/sanjayrj/ai-bot/internal/provider/google"
	"github.com/sanjayrj/ai-bot/internal/store"
)

const stateCookie = "oauth_state"

// Deps wires the sign-in handlers.
type Deps struct {
	Google    *google.Auth
	Sessions  *auth.Sessions
	Users     store.Users
	Allowlist auth.Allowlist
	Logger    log.Logger
}

// Register mounts /auth/google/{start,callback}, /auth/logout and /api/me.
// /api/me sits behind auth.Sessions.Require like the rest of /api.
func Register(mux *http.ServeMux, d Deps) {
	fail := func(w http.ResponseWriter, r *http.Request, code string) {
		http.Redirect(w, r, "/?auth_error="+code, http.StatusFound)
	}

	mux.HandleFunc("GET /auth/google/start", func(w http.ResponseWriter, r *http.Request) {
		if !d.Google.Configured() {
			http.Error(w, "Google sign-in is not configured: set GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET", http.StatusServiceUnavailable)
			return
		}
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		state := hex.EncodeToString(b)
		http.SetCookie(w, &http.Cookie{
			Name: stateCookie, Value: state, Path: "/auth/google", HttpOnly: true,
			Secure: d.Sessions.Secure, SameSite: http.SameSiteLaxMode, Expires: time.Now().Add(10 * time.Minute),
		})
		http.Redirect(w, r, d.Google.AuthCodeURL(state), http.StatusFound)
	})

	mux.HandleFunc("GET /auth/google/callback", func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(stateCookie)
		if err != nil || subtle.ConstantTimeCompare([]byte(c.Value), []byte(r.URL.Query().Get("state"))) != 1 {
			fail(w, r, "expired")
			return
		}
		http.SetCookie(w, &http.Cookie{Name: stateCookie, Path: "/auth/google", MaxAge: -1})
		if r.URL.Query().Get("error") != "" {
			fail(w, r, "denied")
			return
		}

		ctx := r.Context()
		tok, err := d.Google.Exchange(ctx, r.URL.Query().Get("code"))
		if err != nil {
			_ = d.Logger.Log("msg", "google token exchange failed", "err", err)
			fail(w, r, "failed")
			return
		}
		id, err := d.Google.Identify(ctx, tok)
		if err != nil || !id.EmailVerified {
			_ = d.Logger.Log("msg", "google identity check failed", "err", err, "verified", id.EmailVerified)
			fail(w, r, "failed")
			return
		}
		if !d.Allowlist.Allows(id.Email) {
			_ = d.Logger.Log("msg", "sign-in refused: not on allowlist")
			fail(w, r, "not_allowed")
			return
		}

		user, err := d.Users.UpsertUser(ctx, "g:"+id.Sub, id.Email, id.Name)
		if err == nil {
			err = d.Google.SaveToken(ctx, user.ID, tok)
		}
		if err == nil {
			err = d.Sessions.Start(w, r, user.ID)
		}
		if err != nil {
			_ = d.Logger.Log("msg", "sign-in failed", "err", err)
			fail(w, r, "failed")
			return
		}
		_ = d.Logger.Log("msg", "signed in", "user", user.ID)
		http.Redirect(w, r, "/", http.StatusFound)
	})

	mux.HandleFunc("POST /auth/logout", func(w http.ResponseWriter, r *http.Request) {
		d.Sessions.End(w, r)
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("GET /api/me", func(w http.ResponseWriter, r *http.Request) {
		u, err := auth.UserFrom(r.Context())
		if err != nil {
			http.Error(w, `{"error":"Please sign in."}`, http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(u)
	})
}
