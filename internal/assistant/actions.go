package assistant

import (
	"context"

	"github.com/sanjayrj/ai-bot/internal/auth"
	"github.com/sanjayrj/ai-bot/internal/store"
)

// Action statuses (an action's kind is defined by the plugin that runs it).
const (
	StatusPending  = store.StatusPending
	StatusExecuted = store.StatusExecuted
	StatusRejected = store.StatusRejected
	StatusFailed   = store.StatusFailed
)

// Action is a write the agent proposed. Nothing that sends, posts or invites
// runs until a human approves it (unless require_approval is off).
type Action = store.Action

var (
	ErrActionNotFound = store.ErrActionNotFound
	ErrActionResolved = store.ErrActionResolved
)

// NewActionStore returns an in-memory action store (tests, demo, no DB).
func NewActionStore() store.Actions { return store.ActionsView{Memory: store.NewMemory()} }

// userID returns the signed-in user's ID for scoping data.
func userID(ctx context.Context) (string, error) {
	u, err := auth.UserFrom(ctx)
	return u.ID, err
}
