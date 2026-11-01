package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"
)

// ErrNoSession means the session cookie is missing, unknown or expired.
var ErrNoSession = errors.New("not signed in")

// User is a person who signed in with Google.
type User struct {
	ID          string    `json:"id"` // "g:" + Google subject ID
	Email       string    `json:"email"`
	Name        string    `json:"name"`
	CreatedAt   time.Time `json:"created_at"`
	LastLoginAt time.Time `json:"last_login_at"`
}

// Users stores accounts, their encrypted Google tokens, and sessions.
type Users interface {
	// UpsertUser creates or updates the user and records the login.
	UpsertUser(ctx context.Context, id, email, name string) (User, error)
	GetUser(ctx context.Context, id string) (User, error)
	UserByEmail(ctx context.Context, email string) (User, error)

	// GoogleToken returns the sealed (encrypted) OAuth token blob.
	GoogleToken(ctx context.Context, userID string) ([]byte, error)
	SaveGoogleToken(ctx context.Context, userID string, sealed []byte) error

	// Sessions are looked up by the SHA-256 of the cookie value, so a
	// database leak doesn't reveal usable cookies.
	CreateSession(ctx context.Context, userID string, tokenHash []byte, expires time.Time) error
	SessionUser(ctx context.Context, tokenHash []byte) (User, error)
	DeleteSession(ctx context.Context, tokenHash []byte) error
}

// Connection is a user's link to a third-party service (e.g. Slack).
type Connection struct {
	Provider  string    `json:"provider"`
	Label     string    `json:"label"` // e.g. the Slack workspace name
	CreatedAt time.Time `json:"created_at"`
}

// Connections stores per-user, per-provider credentials (sealed/encrypted
// by the caller) for plugins that need their own sign-in.
type Connections interface {
	SaveConnection(ctx context.Context, userID, provider, label string, sealed []byte) error
	// ConnectionSecret returns the sealed credential, or ErrNotConnected.
	ConnectionSecret(ctx context.Context, userID, provider string) ([]byte, error)
	ListConnections(ctx context.Context, userID string) ([]Connection, error)
	DeleteConnection(ctx context.Context, userID, provider string) error
}

// Settings stores small per-user JSON preferences (time rules, sorting
// rules, a learned writing style). Get returns nil, nil when unset.
type Settings interface {
	GetSetting(ctx context.Context, userID, key string) ([]byte, error)
	PutSetting(ctx context.Context, userID, key string, value []byte) error
}

// ErrNotConnected means the user hasn't connected that provider.
var ErrNotConnected = errors.New("not connected")

// NewID returns a random 12-hex-character ID.
func NewID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
