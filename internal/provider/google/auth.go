// Package google implements the Mail, Calendar and Meetings providers on top
// of Google APIs. Every call runs as the signed-in user (from the request
// context), using that user's own OAuth token.
package google

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"golang.org/x/oauth2"
	googleoauth "golang.org/x/oauth2/google"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/meet/v2"

	"github.com/sanjayrj/ai-bot/internal/auth"
	"github.com/sanjayrj/ai-bot/internal/provider"
)

// Scopes requested at sign-in: identity (openid, email, profile), read and
// send mail, manage events, colleagues' free/busy, and Meet transcripts.
var Scopes = []string{
	"openid",
	"email",
	"profile",
	gmail.GmailReadonlyScope,
	gmail.GmailComposeScope,
	gmail.GmailSendScope,
	calendar.CalendarEventsScope,
	calendar.CalendarFreebusyScope,
	meet.MeetingsSpaceReadonlyScope,
}

// TokenStore loads and saves a user's OAuth token (auth.Vault in production).
type TokenStore interface {
	Load(ctx context.Context, userID string) (*oauth2.Token, error)
	Save(ctx context.Context, userID string, t *oauth2.Token) error
}

// Auth runs the OAuth flow and builds per-user API clients.
type Auth struct {
	cfg    *oauth2.Config
	tokens TokenStore
}

// NewAuth returns an Auth. If clientID is empty, Configured reports false.
func NewAuth(clientID, clientSecret, redirectURL string, tokens TokenStore) *Auth {
	return &Auth{
		cfg: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Scopes:       Scopes,
			Endpoint:     googleoauth.Endpoint,
		},
		tokens: tokens,
	}
}

// Configured reports whether OAuth client credentials are present.
func (a *Auth) Configured() bool { return a.cfg.ClientID != "" && a.cfg.ClientSecret != "" }

// Connected reports whether Google calls can be made; per-user tokens are
// created at sign-in, so a signed-in user is always connected.
func (a *Auth) Connected() bool { return a.Configured() }

// AuthCodeURL builds the consent URL. Offline access + consent guarantees a
// refresh token so the assistant keeps working after the access token expires.
func (a *Auth) AuthCodeURL(state string) string {
	return a.cfg.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
}

// Exchange trades an auth code for a token.
func (a *Auth) Exchange(ctx context.Context, code string) (*oauth2.Token, error) {
	return a.cfg.Exchange(ctx, code)
}

// Identity is the signed-in Google account.
type Identity struct {
	Sub           string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
}

// Identify asks Google who the token belongs to.
func (a *Auth) Identify(ctx context.Context, t *oauth2.Token) (Identity, error) {
	var id Identity
	res, err := a.cfg.Client(ctx, t).Get("https://openidconnect.googleapis.com/v1/userinfo")
	if err != nil {
		return id, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return id, fmt.Errorf("userinfo: %s", res.Status)
	}
	if err := json.NewDecoder(res.Body).Decode(&id); err != nil {
		return id, err
	}
	if id.Sub == "" || id.Email == "" {
		return id, fmt.Errorf("userinfo: missing account details")
	}
	return id, nil
}

// SaveToken stores the token for userID (after sign-in).
func (a *Auth) SaveToken(ctx context.Context, userID string, t *oauth2.Token) error {
	return a.tokens.Save(ctx, userID, t)
}

// HTTPClient returns an authorized client for the signed-in user in ctx,
// persisting refreshed tokens.
func (a *Auth) HTTPClient(ctx context.Context) (*http.Client, error) {
	u, err := auth.UserFrom(ctx)
	if err != nil {
		return nil, err
	}
	t, err := a.tokens.Load(ctx, u.ID)
	if err != nil {
		return nil, fmt.Errorf("%w: sign out and sign in again to reconnect Google", provider.ErrNotConnected)
	}
	// Refreshes can happen after the request ends, so persist with a
	// detached context that still carries the user.
	src := &savingSource{
		ctx:    auth.WithUser(context.WithoutCancel(ctx), u),
		store:  a.tokens,
		userID: u.ID,
		last:   t,
		base:   a.cfg.TokenSource(context.Background(), t),
	}
	hc := oauth2.NewClient(ctx, oauth2.ReuseTokenSource(t, src))
	hc.Transport = reauthTransport{base: hc.Transport}
	return hc, nil
}

// reauthTransport turns a rejected token refresh into provider.ErrReauth,
// so callers show "sign in again" instead of a raw OAuth error. This is
// what happens weekly while the OAuth app is in Testing mode.
type reauthTransport struct{ base http.RoundTripper }

func (t reauthTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	res, err := t.base.RoundTrip(r)
	var re *oauth2.RetrieveError
	if err != nil && errors.As(err, &re) {
		switch re.ErrorCode {
		case "invalid_grant", "unauthorized_client", "invalid_client":
			return nil, provider.ErrReauth
		}
	}
	return res, err
}

// savingSource writes the token back whenever it is refreshed.
type savingSource struct {
	ctx    context.Context
	store  TokenStore
	userID string
	base   oauth2.TokenSource

	mu   sync.Mutex
	last *oauth2.Token
}

func (s *savingSource) Token() (*oauth2.Token, error) {
	t, err := s.base.Token()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.AccessToken != s.last.AccessToken {
		if t.RefreshToken == "" {
			t.RefreshToken = s.last.RefreshToken
		}
		_ = s.store.Save(s.ctx, s.userID, t)
		s.last = t
	}
	return t, nil
}
