package assistant

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/joho/godotenv"

	"github.com/sanjayrj/ai-bot/internal/auth"
	"github.com/sanjayrj/ai-bot/internal/llm"
	"github.com/sanjayrj/ai-bot/internal/provider/mock"
	"github.com/sanjayrj/ai-bot/internal/store"
)

// TestLiveAddAttendee checks that the real model (from .env) edits the
// existing meeting instead of only emailing. Runs on demo data, never on a
// real account. Opt-in: LIVE_LLM=1 go test ./internal/assistant -run Live -v
func TestLiveAddAttendee(t *testing.T) {
	if os.Getenv("LIVE_LLM") != "1" {
		t.Skip("set LIVE_LLM=1 to call the real model from .env")
	}
	_ = godotenv.Load("../../.env")
	model, err := llm.New(llm.Config{
		Provider: os.Getenv("LLM_PROVIDER"), APIKey: os.Getenv("LLM_API_KEY"),
		Model: os.Getenv("LLM_MODEL"), BaseURL: os.Getenv("LLM_BASE_URL"),
	})
	if err != nil {
		t.Fatal(err)
	}
	loc := time.UTC
	ws := mock.New(time.Now().In(loc))
	tools := NewToolbox(ws.Mail(), ws.Calendar(), ws.Meetings(), NewActionStore(), loc, true)
	agent := NewAgent(model, tools, loc, nil)

	ctx, cancel := context.WithTimeout(auth.WithUser(context.Background(), store.User{ID: "live"}), 3*time.Minute)
	defer cancel()
	_, reply, calls, err := agent.Run(ctx, nil, "Add new@example.com to my 1:1 with Sara today and send them a mail telling them to join.")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	var sawUpdate bool
	for _, c := range calls {
		names = append(names, c.Name)
		if c.Name == "update_event" && strings.Contains(c.Input, "new@example.com") && !c.IsError {
			sawUpdate = true
		}
	}
	t.Logf("tools: %v\nreply: %s", names, reply)
	if !sawUpdate {
		t.Fatalf("model did not add the attendee with update_event; tools used: %v", names)
	}
}

// TestLiveColleaguesAndSummary checks the model picks the colleague and
// transcript tools. Opt-in like TestLiveAddAttendee.
func TestLiveColleaguesAndSummary(t *testing.T) {
	if os.Getenv("LIVE_LLM") != "1" {
		t.Skip("set LIVE_LLM=1 to call the real model from .env")
	}
	_ = godotenv.Load("../../.env")
	model, err := llm.New(llm.Config{
		Provider: os.Getenv("LLM_PROVIDER"), APIKey: os.Getenv("LLM_API_KEY"),
		Model: os.Getenv("LLM_MODEL"), BaseURL: os.Getenv("LLM_BASE_URL"),
	})
	if err != nil {
		t.Fatal(err)
	}
	loc := time.UTC
	ws := mock.New(time.Now().In(loc))
	agent := NewAgent(model, NewToolbox(ws.Mail(), ws.Calendar(), ws.Meetings(), NewActionStore(), loc, true), loc, nil)

	for _, c := range []struct{ ask, want string }{
		{"When are sara@example.com and marcus@example.com both free tomorrow for an hour?", "find_common_free_slots"},
		{"Summarize today's Exec staff sync: decisions and action items.", "get_meeting_transcript"},
	} {
		ctx, cancel := context.WithTimeout(auth.WithUser(context.Background(), store.User{ID: "live"}), 2*time.Minute)
		_, reply, calls, err := agent.Run(ctx, nil, c.ask)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, call := range calls {
			names = append(names, call.Name)
		}
		t.Logf("%q\n  tools: %v\n  reply: %s", c.ask, names, reply)
		if !strings.Contains(strings.Join(names, ","), c.want) {
			t.Errorf("expected %s, got %v", c.want, names)
		}
	}
}
