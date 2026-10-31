package llm

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/joho/godotenv"
)

// TestLiveToolRoundTrip calls the model configured in the repo's .env with a
// dummy tool. Opt-in: LIVE_LLM=1 go test ./internal/llm -run Live -v
func TestLiveToolRoundTrip(t *testing.T) {
	if os.Getenv("LIVE_LLM") != "1" {
		t.Skip("set LIVE_LLM=1 to call the real model from .env")
	}
	_ = godotenv.Load("../../.env")
	cfg := Config{
		Provider: os.Getenv("LLM_PROVIDER"), APIKey: os.Getenv("LLM_API_KEY"),
		Model: os.Getenv("LLM_MODEL"), BaseURL: os.Getenv("LLM_BASE_URL"),
	}
	client, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	tools := []ToolSpec{{
		Name:        "get_secret_word",
		Description: "Returns today's secret word. Always call this when asked for the secret word.",
		Properties:  map[string]any{},
	}}
	msgs := []Message{{Role: RoleUser, Text: "What is today's secret word? Use the tool, then reply with just the word."}}

	resp, err := client.Complete(ctx, Request{System: "You are a test assistant.", Messages: msgs, Tools: tools})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Message.ToolCalls) == 0 {
		t.Fatalf("model did not call the tool; replied %q", resp.Message.Text)
	}
	msgs = append(msgs, resp.Message)
	var results []ToolResult
	for _, tc := range resp.Message.ToolCalls {
		results = append(results, ToolResult{CallID: tc.ID, Content: `{"word":"pineapple"}`})
	}
	msgs = append(msgs, Message{Role: RoleUser, ToolResults: results})

	final, err := client.Complete(ctx, Request{System: "You are a test assistant.", Messages: msgs, Tools: tools})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(final.Message.Text), "pineapple") {
		t.Fatalf("unexpected final answer %q", final.Message.Text)
	}
	t.Logf("model %s ok: %q", cfg.Model, final.Message.Text)
}
