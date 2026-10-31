// Package store persists conversations: the model-facing message history
// (needed to resume a chat) and the user-facing turns (shown in the UI).
package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/sanjayrj/ai-bot/internal/llm"
)

// ErrNotFound is returned for an unknown conversation ID.
var ErrNotFound = errors.New("conversation not found")

// Summary is one row of the conversation list.
type Summary struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Turns     int       `json:"turns"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Turn is one user message and the assistant's answer, as shown in the UI.
type Turn struct {
	UserText  string          `json:"user_text"`
	Reply     string          `json:"reply"`
	ToolCalls json.RawMessage `json:"tool_calls,omitempty"`
	Error     string          `json:"error,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

// Conversation is a full transcript.
type Conversation struct {
	Summary
	Turns []Turn `json:"turns"`
}

// Store is implemented by Memory and Postgres. Every method is scoped to
// the owning user; a conversation ID owned by someone else behaves as if it
// doesn't exist.
type Store interface {
	List(ctx context.Context, userID string, limit int) ([]Summary, error)
	Get(ctx context.Context, userID, id string) (Conversation, error)
	// History returns the model messages to resume the conversation.
	History(ctx context.Context, userID, id string) ([]llm.Message, error)
	// AppendTurn creates the conversation if needed, then appends the new
	// model messages and the UI turn atomically.
	AppendTurn(ctx context.Context, userID, id string, msgs []llm.Message, turn Turn) error
	Delete(ctx context.Context, userID, id string) error
	Close()
}

// TitleFrom derives a conversation title from its first message.
func TitleFrom(text string) string {
	t := strings.Join(strings.Fields(text), " ")
	if r := []rune(t); len(r) > 60 {
		t = string(r[:60]) + "…"
	}
	if t == "" {
		t = "New conversation"
	}
	return t
}
