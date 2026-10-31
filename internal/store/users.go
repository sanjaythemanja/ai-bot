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

// NewID returns a random 12-hex-character ID.
func NewID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
