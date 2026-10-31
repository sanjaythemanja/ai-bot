package authhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-kit/log"

	"github.com/sanjayrj/ai-bot/internal/auth"
	"github.com/sanjayrj/ai-bot/internal/secure"
	"github.com/sanjayrj/ai-bot/internal/store"
)

func TestSlackConnectFlow(t *testing.T) {
	// Fake Slack token endpoint.
	slack := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("code") != "good-code" || r.Form.Get("client_secret") != "shh" {
			_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_code"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"authed_user":{"access_token":"xoxp-user-token"},"team":{"name":"Acme"}}`))
	}))
	defer slack.Close()

	mem := store.NewMemory()
	u, _ := mem.UpsertUser(context.Background(), "g:1", "me@example.com", "Me")
	box, _ := secure.NewBox("0123456789abcdef0123456789abcdef-test")
	sessions := &auth.Sessions{Users: mem, TTL: time.Hour}
	vault := auth.ConnVault{Conns: mem, Box: box}

	mux := http.NewServeMux()
	RegisterConnections(mux, ConnDeps{
		Slack:    SlackOAuth{ClientID: "cid", ClientSecret: "shh", RedirectURL: "http://app/auth/slack/callback", AuthorizeURL: "https://slack.example/authorize", TokenURL: slack.URL},
		Sessions: sessions, Vault: vault, Logger: log.NewNopLogger(),
	})
	srv := httptest.NewServer(sessions.Require(mux))
	defer srv.Close()
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	// Sign in.
	rec := httptest.NewRecorder()
	_ = sessions.Start(rec, httptest.NewRequest("GET", "/", nil), u.ID)
	session := rec.Result().Cookies()[0]

	// Start → redirect to Slack with user scopes and a state cookie.
	req, _ := http.NewRequest("GET", srv.URL+"/auth/slack/start", nil)
	req.AddCookie(session)
	res, err := noFollow.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	loc, _ := url.Parse(res.Header.Get("Location"))
	if !strings.HasPrefix(loc.String(), "https://slack.example/authorize") || !strings.Contains(loc.Query().Get("user_scope"), "chat:write") {
		t.Fatalf("bad authorize redirect: %s", loc)
	}
	var state *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == slackStateCookie {
			state = c
		}
	}

	callback := func(code, st string) string {
		req, _ := http.NewRequest("GET", srv.URL+"/auth/slack/callback?code="+code+"&state="+st, nil)
		req.AddCookie(session)
		req.AddCookie(state)
		res, err := noFollow.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res.Header.Get("Location")
	}
	if got := callback("good-code", "forged"); !strings.Contains(got, "slack_expired") {
		t.Fatalf("forged state must be refused, got %s", got)
	}
	if got := callback("good-code", loc.Query().Get("state")); got != "/?connected=slack" {
		t.Fatalf("expected success redirect, got %s", got)
	}

	// Token is stored encrypted and readable only through the vault.
	sealed, _ := mem.ConnectionSecret(context.Background(), u.ID, "slack")
	if strings.Contains(string(sealed), "xoxp-user-token") {
		t.Fatal("slack token stored in plaintext")
	}
	if tok, err := vault.Load(auth.WithUser(context.Background(), u), "slack"); err != nil || tok != "xoxp-user-token" {
		t.Fatalf("vault load: %q %v", tok, err)
	}

	// /api/connections reports it; disconnect removes it.
	conns := func() map[string]any {
		req, _ := http.NewRequest("GET", srv.URL+"/api/connections", nil)
		req.AddCookie(session)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out struct{ Connections []map[string]any }
		_ = json.NewDecoder(res.Body).Decode(&out)
		return out.Connections[0]
	}
	if c := conns(); c["connected"] != true || c["label"] != "Acme" {
		t.Fatalf("expected connected to Acme, got %v", c)
	}
	req, _ = http.NewRequest("POST", srv.URL+"/auth/slack/disconnect", nil)
	req.AddCookie(session)
	_, _ = http.DefaultClient.Do(req)
	if c := conns(); c["connected"] != false {
		t.Fatalf("expected disconnected, got %v", c)
	}

	// Not signed in: start bounces home instead of starting OAuth.
	res, err = noFollow.Get(srv.URL + "/auth/slack/start")
	if err != nil {
		t.Fatal(err)
	}
	if res.Header.Get("Location") != "/?" {
		t.Fatalf("signed-out start should go home, got %q", res.Header.Get("Location"))
	}
}
