package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/sanjayrj/ai-bot/internal/llm"
)

// Runs only against a real database: TEST_DATABASE_URL=postgres://... go test ./internal/store
func openTestPG(t *testing.T) *Postgres {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run against PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pg, err := OpenPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pg.Close)
	return pg
}

func TestPostgresConversationsScopedByUser(t *testing.T) {
	pg := openTestPG(t)
	ctx := context.Background()
	suffix := time.Now().Format("150405.000000")
	alice, err := pg.UpsertUser(ctx, "test-a-"+suffix, "a-"+suffix+"@example.com", "Alice")
	if err != nil {
		t.Fatal(err)
	}
	bob, _ := pg.UpsertUser(ctx, "test-b-"+suffix, "b-"+suffix+"@example.com", "Bob")
	t.Cleanup(func() {
		_, _ = pg.pool.Exec(context.Background(), `DELETE FROM users WHERE id = ANY($1)`, []string{alice.ID, bob.ID})
	})

	id := "conv-" + suffix
	native := &llm.Native{Kind: "openai", Raw: json.RawMessage(`{"role":"assistant","content":"hi"}`)}
	msgs := []llm.Message{{Role: llm.RoleUser, Text: "hello"}, {Role: llm.RoleAssistant, Text: "hi", Native: native}}
	if err := pg.AppendTurn(ctx, alice.ID, id, msgs, Turn{UserText: "hello", Reply: "hi", ToolCalls: json.RawMessage(`[{"name":"x"}]`)}); err != nil {
		t.Fatal(err)
	}
	if err := pg.AppendTurn(ctx, alice.ID, id, []llm.Message{{Role: llm.RoleUser, Text: "again"}}, Turn{UserText: "again", Error: "boom"}); err != nil {
		t.Fatal(err)
	}

	hist, err := pg.History(ctx, alice.ID, id)
	if err != nil || len(hist) != 3 || hist[1].Native == nil || hist[2].Text != "again" {
		t.Fatalf("history wrong: %+v err=%v", hist, err)
	}
	conv, err := pg.Get(ctx, alice.ID, id)
	if err != nil || len(conv.Turns) != 2 || conv.Title != "hello" || conv.Turns[1].Error != "boom" {
		t.Fatalf("conversation wrong: %+v err=%v", conv, err)
	}

	// Bob can't see, read, extend or delete Alice's conversation.
	if list, _ := pg.List(ctx, bob.ID, 50); len(list) != 0 {
		t.Fatalf("bob sees alice's conversations: %+v", list)
	}
	if _, err := pg.Get(ctx, bob.ID, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob read alice's conversation: %v", err)
	}
	if h, _ := pg.History(ctx, bob.ID, id); len(h) != 0 {
		t.Fatal("bob read alice's history")
	}
	if err := pg.AppendTurn(ctx, bob.ID, id, nil, Turn{UserText: "hijack"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob appended to alice's conversation: %v", err)
	}
	if err := pg.Delete(ctx, bob.ID, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob deleted alice's conversation: %v", err)
	}
	if err := pg.Delete(ctx, alice.ID, id); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresUsersSessionsActions(t *testing.T) {
	pg := openTestPG(t)
	ctx := context.Background()
	suffix := time.Now().Format("150405.000000")
	u, err := pg.UpsertUser(ctx, "test-u-"+suffix, "U-"+suffix+"@Example.com", "U")
	if err != nil || u.Email != "u-"+suffix+"@example.com" {
		t.Fatalf("upsert: %+v %v", u, err)
	}
	other, _ := pg.UpsertUser(ctx, "test-o-"+suffix, "o-"+suffix+"@example.com", "O")
	t.Cleanup(func() {
		_, _ = pg.pool.Exec(context.Background(), `DELETE FROM users WHERE id = ANY($1)`, []string{u.ID, other.ID})
	})

	// Tokens
	if _, err := pg.GoogleToken(ctx, u.ID); !errors.Is(err, ErrNoSession) {
		t.Fatalf("expected no token yet, got %v", err)
	}
	_ = pg.SaveGoogleToken(ctx, u.ID, []byte("sealed"))
	if b, err := pg.GoogleToken(ctx, u.ID); err != nil || string(b) != "sealed" {
		t.Fatalf("token round trip: %q %v", b, err)
	}

	// Sessions, including expiry and delete.
	hash := []byte("hash-" + suffix)
	_ = pg.CreateSession(ctx, u.ID, hash, time.Now().Add(time.Hour))
	if got, err := pg.SessionUser(ctx, hash); err != nil || got.ID != u.ID {
		t.Fatalf("session lookup: %+v %v", got, err)
	}
	old := []byte("old-" + suffix)
	_ = pg.CreateSession(ctx, u.ID, old, time.Now().Add(-time.Minute))
	if _, err := pg.SessionUser(ctx, old); !errors.Is(err, ErrNoSession) {
		t.Fatal("expired session accepted")
	}
	_ = pg.DeleteSession(ctx, hash)
	if _, err := pg.SessionUser(ctx, hash); !errors.Is(err, ErrNoSession) {
		t.Fatal("deleted session accepted")
	}

	// Actions: claim once, scoped to owner.
	acts := pg.Actions()
	a, err := acts.Add(ctx, u.ID, "send_email", "Email x", map[string]any{"to": []string{"x@example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acts.Claim(ctx, other.ID, a.ID, StatusExecuted); !errors.Is(err, ErrActionNotFound) {
		t.Fatalf("other user claimed the action: %v", err)
	}
	if _, err := acts.UpdatePending(ctx, u.ID, a.ID, "Email y", map[string]any{"to": []string{"y@example.com"}}); err != nil {
		t.Fatal(err)
	}
	if got, err := acts.Claim(ctx, u.ID, a.ID, StatusExecuted); err != nil || got.Status != StatusExecuted || got.Summary != "Email y" {
		t.Fatalf("claim: %+v %v", got, err)
	}
	if _, err := acts.Claim(ctx, u.ID, a.ID, StatusExecuted); !errors.Is(err, ErrActionResolved) {
		t.Fatalf("double claim allowed: %v", err)
	}
	if f, err := acts.Finish(ctx, u.ID, a.ID, StatusExecuted, "sent", ""); err != nil || f.Result != "sent" {
		t.Fatalf("finish: %+v %v", f, err)
	}
	if list, _ := acts.List(ctx, other.ID, 10); len(list) != 0 {
		t.Fatal("other user sees actions")
	}
}

func TestPostgresConnections(t *testing.T) {
	pg := openTestPG(t)
	ctx := context.Background()
	suffix := time.Now().Format("150405.000000")
	u, _ := pg.UpsertUser(ctx, "test-c-"+suffix, "c-"+suffix+"@example.com", "C")
	t.Cleanup(func() { _, _ = pg.pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, u.ID) })

	if _, err := pg.ConnectionSecret(ctx, u.ID, "slack"); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("expected not connected, got %v", err)
	}
	_ = pg.SaveConnection(ctx, u.ID, "slack", "Acme", []byte("sealed-1"))
	_ = pg.SaveConnection(ctx, u.ID, "slack", "Acme 2", []byte("sealed-2")) // reconnect replaces
	if b, err := pg.ConnectionSecret(ctx, u.ID, "slack"); err != nil || string(b) != "sealed-2" {
		t.Fatalf("secret: %q %v", b, err)
	}
	list, _ := pg.ListConnections(ctx, u.ID)
	if len(list) != 1 || list[0].Label != "Acme 2" {
		t.Fatalf("list: %+v", list)
	}
	_ = pg.DeleteConnection(ctx, u.ID, "slack")
	if _, err := pg.ConnectionSecret(ctx, u.ID, "slack"); !errors.Is(err, ErrNotConnected) {
		t.Fatal("connection not deleted")
	}
}
