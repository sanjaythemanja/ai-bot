package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Action statuses.
const (
	StatusPending  = "pending"
	StatusExecuted = "executed"
	StatusRejected = "rejected"
	StatusFailed   = "failed"
)

var (
	ErrActionNotFound = errors.New("action not found")
	ErrActionResolved = errors.New("action already resolved")
)

// Action is a write the assistant proposed (send an email, change an event).
// Nothing outward-facing runs until it is approved.
type Action struct {
	ID        string          `json:"id"`
	UserID    string          `json:"-"`
	Kind      string          `json:"kind"`
	Summary   string          `json:"summary"`
	Payload   json.RawMessage `json:"payload"`
	Status    string          `json:"status"`
	Result    string          `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// Actions stores each user's actions. Every method is scoped to userID, so
// one user can never see or resolve another's actions.
type Actions interface {
	Add(ctx context.Context, userID, kind, summary string, payload any) (Action, error)
	Get(ctx context.Context, userID, id string) (Action, error)
	List(ctx context.Context, userID string, limit int) ([]Action, error)
	// Claim moves a pending action to status exactly once; a second caller
	// gets ErrActionResolved.
	Claim(ctx context.Context, userID, id, status string) (Action, error)
	UpdatePending(ctx context.Context, userID, id, summary string, payload any) (Action, error)
	Finish(ctx context.Context, userID, id, status, result, errMsg string) (Action, error)
}
