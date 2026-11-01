package authhttp

import (
	"cmp"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-kit/log"

	"github.com/sanjayrj/ai-bot/internal/auth"
)

// SlackUserScopes lets the assistant act as the user: read their channels,
// DMs and search, and post as them (after approval).
var SlackUserScopes = []string{
	"channels:read", "groups:read", "im:read", "mpim:read",
	"channels:history", "groups:history", "im:history", "mpim:history",
	"users:read", "search:read", "chat:write",
}

// SlackOAuth configures "Connect Slack". Empty ClientID disables it.
type SlackOAuth struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	// AuthorizeURL and TokenURL default to Slack's; tests override them.
	AuthorizeURL string
	TokenURL     string
}

func (s SlackOAuth) Configured() bool { return s.ClientID != "" && s.ClientSecret != "" }

// ConnDeps wires the third-party connection endpoints.
type ConnDeps struct {
	Slack    SlackOAuth
	Sessions *auth.Sessions
	Vault    auth.ConnVault
	Logger   log.Logger
	// DemoLabel, when set, reports Slack as connected to a sample workspace.
	DemoLabel string
}

const slackStateCookie = "slack_oauth_state"

// RegisterConnections mounts Slack connect/disconnect and GET /api/connections.
func RegisterConnections(mux *http.ServeMux, d ConnDeps) {
	if d.Slack.AuthorizeURL == "" {
		d.Slack.AuthorizeURL = "https://slack.com/oauth/v2/authorize"
	}
	if d.Slack.TokenURL == "" {
		d.Slack.TokenURL = "https://slack.com/api/oauth.v2.access"
	}
	back := func(w http.ResponseWriter, r *http.Request, q string) { http.Redirect(w, r, "/?"+q, http.StatusFound) }

	mux.HandleFunc("GET /auth/slack/start", func(w http.ResponseWriter, r *http.Request) {
		if !d.Slack.Configured() {
			http.Error(w, "Slack is not configured: set SLACK_CLIENT_ID and SLACK_CLIENT_SECRET", http.StatusServiceUnavailable)
			return
		}
		if _, err := d.Sessions.UserFromRequest(r); err != nil {
			back(w, r, "")
			return
		}
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		state := hex.EncodeToString(b)
		http.SetCookie(w, &http.Cookie{
			Name: slackStateCookie, Value: state, Path: "/auth/slack", HttpOnly: true,
			Secure: d.Sessions.Secure, SameSite: http.SameSiteLaxMode, Expires: time.Now().Add(10 * time.Minute),
		})
		q := url.Values{
			"client_id":    {d.Slack.ClientID},
			"user_scope":   {strings.Join(SlackUserScopes, ",")},
			"redirect_uri": {d.Slack.RedirectURL},
			"state":        {state},
		}
		http.Redirect(w, r, d.Slack.AuthorizeURL+"?"+q.Encode(), http.StatusFound)
	})

	mux.HandleFunc("GET /auth/slack/callback", func(w http.ResponseWriter, r *http.Request) {
		u, err := d.Sessions.UserFromRequest(r)
		if err != nil {
			back(w, r, "")
			return
		}
		c, err := r.Cookie(slackStateCookie)
		if err != nil || subtle.ConstantTimeCompare([]byte(c.Value), []byte(r.URL.Query().Get("state"))) != 1 {
			back(w, r, "connect_error=slack_expired")
			return
		}
		http.SetCookie(w, &http.Cookie{Name: slackStateCookie, Path: "/auth/slack", MaxAge: -1})
		if r.URL.Query().Get("error") != "" {
			back(w, r, "connect_error=slack_denied")
			return
		}
		token, team, err := d.Slack.exchange(r, r.URL.Query().Get("code"))
		if err == nil {
			err = d.Vault.Save(r.Context(), u.ID, "slack", team, token)
		}
		if err != nil {
			_ = d.Logger.Log("msg", "slack connect failed", "user", u.ID, "err", err)
			back(w, r, "connect_error=slack_failed")
			return
		}
		_ = d.Logger.Log("msg", "slack connected", "user", u.ID)
		back(w, r, "connected=slack")
	})

	mux.HandleFunc("POST /auth/slack/disconnect", func(w http.ResponseWriter, r *http.Request) {
		u, err := d.Sessions.UserFromRequest(r)
		if err != nil {
			http.Error(w, `{"error":"Please sign in."}`, http.StatusUnauthorized)
			return
		}
		if err := d.Vault.Conns.DeleteConnection(r.Context(), u.ID, "slack"); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	// GET /api/connections sits behind Sessions.Require like the rest of /api.
	mux.HandleFunc("GET /api/connections", func(w http.ResponseWriter, r *http.Request) {
		u, err := auth.UserFrom(r.Context())
		if err != nil {
			http.Error(w, `{"error":"Please sign in."}`, http.StatusUnauthorized)
			return
		}
		conns, err := d.Vault.Conns.ListConnections(r.Context(), u.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		slack := map[string]any{"provider": "slack", "configured": d.Slack.Configured(), "connected": false}
		if d.DemoLabel != "" {
			slack["configured"], slack["connected"], slack["label"] = true, true, d.DemoLabel
		}
		for _, c := range conns {
			if c.Provider == "slack" {
				slack["connected"], slack["label"] = true, c.Label
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"connections": []any{slack}})
	})
}

// exchange trades a code for the user's Slack token and workspace name.
func (s SlackOAuth) exchange(r *http.Request, code string) (token, team string, err error) {
	form := url.Values{"client_id": {s.ClientID}, "client_secret": {s.ClientSecret}, "code": {code}, "redirect_uri": {s.RedirectURL}}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, s.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer res.Body.Close()
	var out struct {
		OK         bool   `json:"ok"`
		Error      string `json:"error"`
		AuthedUser struct {
			AccessToken string `json:"access_token"`
		} `json:"authed_user"`
		Team struct {
			Name string `json:"name"`
		} `json:"team"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return "", "", err
	}
	if !out.OK || out.AuthedUser.AccessToken == "" {
		return "", "", fmt.Errorf("slack oauth: %s", cmp.Or(out.Error, "no user token returned"))
	}
	return out.AuthedUser.AccessToken, out.Team.Name, nil
}
