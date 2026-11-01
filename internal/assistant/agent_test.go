package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sanjayrj/ai-bot/internal/auth"
	"github.com/sanjayrj/ai-bot/internal/llm"
	"github.com/sanjayrj/ai-bot/internal/plugin"
	"github.com/sanjayrj/ai-bot/internal/provider/mock"
	"github.com/sanjayrj/ai-bot/internal/store"
)

// scriptModel answers each call with the next scripted response. Requests
// without tools (condense calls) are answered by condense.
type scriptModel struct {
	mu        sync.Mutex
	turns     []llm.Response
	requests  []llm.Request
	condensed atomic.Int32
}

func (m *scriptModel) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(req.Tools) == 0 {
		m.condensed.Add(1)
		return llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Text: "SHORT SUMMARY"}, Stop: llm.StopEnd}, nil
	}
	m.requests = append(m.requests, req)
	if len(m.turns) == 0 {
		return llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Text: "done"}, Stop: llm.StopEnd}, nil
	}
	r := m.turns[0]
	m.turns = m.turns[1:]
	return r, nil
}

func toolCalls(calls ...[2]string) llm.Response {
	msg := llm.Message{Role: llm.RoleAssistant}
	for i, c := range calls {
		msg.ToolCalls = append(msg.ToolCalls, llm.ToolCall{ID: fmt.Sprintf("c%d", i+1), Name: c[0], Input: json.RawMessage(c[1])})
	}
	return llm.Response{Message: msg, Stop: llm.StopToolUse}
}

// slowPlugin has a read that sleeps, a read that returns a huge result and a
// write, to observe how the agent schedules them.
type slowPlugin struct {
	running, peak atomic.Int32
	order         []string
	mu            sync.Mutex
}

func (p *slowPlugin) Name() string { return "slow" }
func (p *slowPlugin) Tools() []plugin.Tool {
	return []plugin.Tool{
		{Name: "slow_read", Properties: map[string]any{"meeting_code": plugin.Str("code")}, Run: func(ctx context.Context, raw json.RawMessage) (any, error) {
			n := p.running.Add(1)
			for {
				if old := p.peak.Load(); n <= old || p.peak.CompareAndSwap(old, n) {
					break
				}
			}
			time.Sleep(100 * time.Millisecond)
			p.running.Add(-1)
			in, _ := plugin.Decode[struct {
				Code string `json:"meeting_code"`
			}](raw)
			return "transcript of " + in.Code, nil
		}},
		{Name: "huge_read", Run: func(context.Context, json.RawMessage) (any, error) {
			return strings.Repeat("blah ", 5000), nil
		}},
		{Name: "note_write", Mutates: true, Run: func(_ context.Context, raw json.RawMessage) (any, error) {
			p.mu.Lock()
			p.order = append(p.order, string(raw))
			p.mu.Unlock()
			return "ok", nil
		}},
	}
}

func agentWith(t *testing.T, m *scriptModel, p plugin.Plugin) (*Agent, context.Context) {
	t.Helper()
	tools := NewToolbox(NewActionStore(), true, p)
	ctx := auth.WithUser(context.Background(), store.User{ID: "u1"})
	return NewAgent(m, tools, time.UTC, nil), ctx
}

func TestReadsRunInParallelInOrder(t *testing.T) {
	m := &scriptModel{turns: []llm.Response{toolCalls(
		[2]string{"slow_read", `{"meeting_code":"a"}`}, [2]string{"slow_read", `{"meeting_code":"b"}`},
		[2]string{"slow_read", `{"meeting_code":"c"}`}, [2]string{"slow_read", `{"meeting_code":"d"}`},
		[2]string{"slow_read", `{"meeting_code":"e"}`})}}
	p := &slowPlugin{}
	a, ctx := agentWith(t, m, p)

	var mu sync.Mutex
	var steps []Step
	start := time.Now()
	_, _, calls, err := a.RunWithProgress(ctx, nil, "summarise five meetings", func(s Step) { mu.Lock(); steps = append(steps, s); mu.Unlock() })
	took := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	// 5 × 100 ms, at most 4 at once → about 200 ms, not 500.
	if took > 350*time.Millisecond || p.peak.Load() != 4 {
		t.Fatalf("reads should run 4 at a time: took %s, peak %d", took, p.peak.Load())
	}
	for i, want := range []string{"a", "b", "c", "d", "e"} {
		if calls[i].Output != `"transcript of `+want+`"` {
			t.Fatalf("result %d out of order: %s", i, calls[i].Output)
		}
	}
	// The second model call gets all five results, in order.
	last := m.requests[1].Messages[len(m.requests[1].Messages)-1]
	if len(last.ToolResults) != 5 || last.ToolResults[4].CallID != "c5" {
		t.Fatalf("tool results not returned together in order: %+v", last.ToolResults)
	}
	done := 0
	for _, s := range steps {
		if s.Kind == StepTool && s.Status == StepDone && s.Hint != "" {
			done++
		}
	}
	if done != 5 {
		t.Fatalf("expected 5 finished tool steps with hints, got %d: %+v", done, steps)
	}
}

func TestStateChangingToolsRunAloneInOrder(t *testing.T) {
	m := &scriptModel{turns: []llm.Response{toolCalls(
		[2]string{"slow_read", `{"meeting_code":"a"}`}, [2]string{"note_write", `"first"`},
		[2]string{"slow_read", `{"meeting_code":"b"}`}, [2]string{"note_write", `"second"`})}}
	p := &slowPlugin{}
	a, ctx := agentWith(t, m, p)
	if _, _, _, err := a.Run(ctx, nil, "go"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(p.order, ",") != `"first","second"` {
		t.Fatalf("writes ran out of order: %v", p.order)
	}
}

func TestDuplicateWritesInOneTurnAreCaught(t *testing.T) {
	// Two identical emails in one model turn: run one by one, the second is
	// refused by the duplicate check rather than racing past it.
	email := `{"to":["dana@example.com"],"subject":"Hi","body":"Hello"}`
	m := &scriptModel{turns: []llm.Response{toolCalls([2]string{"send_email", email}, [2]string{"send_email", email})}}
	a, ctx := agentWith(t, m, demoPlugins(mockWorkspace(), time.UTC)[0])
	_, _, calls, err := a.Run(ctx, nil, "email dana")
	if err != nil {
		t.Fatal(err)
	}
	if calls[0].IsError || !calls[1].IsError || !strings.Contains(calls[1].Output, "already waiting") {
		t.Fatalf("second identical email should be refused: %+v", calls)
	}
}

func TestLargeResultsAreCondensed(t *testing.T) {
	m := &scriptModel{turns: []llm.Response{toolCalls([2]string{"huge_read", `{}`}, [2]string{"slow_read", `{"meeting_code":"a"}`})}}
	a, ctx := agentWith(t, m, &slowPlugin{})
	var summarizing atomic.Int32
	_, _, calls, err := a.RunWithProgress(ctx, nil, "what matters?", func(s Step) {
		if s.Status == StepSummarizing {
			summarizing.Add(1)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.condensed.Load() != 1 || summarizing.Load() != 1 {
		t.Fatalf("exactly the large result should be condensed (calls=%d)", m.condensed.Load())
	}
	if !strings.Contains(calls[0].Output, "SHORT SUMMARY") || strings.Contains(calls[1].Output, "Summary of") {
		t.Fatalf("unexpected outputs: %q / %q", calls[0].Output[:60], calls[1].Output)
	}
}

func mockWorkspace() *mock.Workspace { return mock.New(time.Now().UTC()) }

func text(s string) llm.Response {
	return llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Text: s}, Stop: llm.StopEnd}
}

func TestClaimedButUndoneActionIsCorrected(t *testing.T) {
	// The model first claims the email was sent without calling a tool; the
	// check makes it act, so the email really ends up waiting for approval.
	m := &scriptModel{turns: []llm.Response{
		text("Done! I've sent the email to Rakshit."),
		toolCalls([2]string{"send_email", `{"to":["rakshit@example.com"],"subject":"Run","body":"See you at 6.\nSanjay"}`}),
		text("The email to Rakshit is waiting for your approval."),
	}}
	tools := NewToolbox(NewActionStore(), true, demoPlugins(mockWorkspace(), time.UTC)...)
	ctx := auth.WithUser(context.Background(), store.User{ID: "u1", Name: "Sanjay"})
	a := NewAgent(m, tools, time.UTC, nil)
	_, reply, calls, err := a.Run(ctx, nil, "send Rakshit an email about the run")
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].Name != "send_email" || calls[0].IsError || !strings.Contains(reply, "waiting for your approval") {
		t.Fatalf("expected the claim to be turned into a real send_email: reply=%q calls=%+v", reply, calls)
	}
	if got := m.requests[1].Messages[len(m.requests[1].Messages)-1].Text; !strings.Contains(got, "Automatic check") {
		t.Fatalf("the correction prompt was not sent: %q", got)
	}
	// The user's name (not their email) is given to the model for sign-offs.
	if first := m.requests[0].Messages[0].Text; !strings.Contains(first, "the user's name: Sanjay") {
		t.Fatalf("name missing from the prompt: %q", first)
	}
}

func TestTruthfulRepliesAreNotChecked(t *testing.T) {
	m := &scriptModel{turns: []llm.Response{
		toolCalls([2]string{"send_email", `{"to":["rakshit@example.com"],"subject":"Run","body":"See you."}`}),
		text("I've queued the email for your approval."),
	}}
	tools := NewToolbox(NewActionStore(), true, demoPlugins(mockWorkspace(), time.UTC)...)
	ctx := auth.WithUser(context.Background(), store.User{ID: "u1"})
	if _, _, _, err := NewAgent(m, tools, time.UTC, nil).Run(ctx, nil, "email Rakshit"); err != nil {
		t.Fatal(err)
	}
	if len(m.requests) != 2 {
		t.Fatalf("a claim backed by a real tool call must not trigger a check (%d model calls)", len(m.requests))
	}
}
