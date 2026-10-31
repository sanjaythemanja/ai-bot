package llm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// scripted returns the given errors in order, then succeeds.
type scripted struct {
	name  string
	errs  []error
	calls atomic.Int32
}

func (s *scripted) Complete(context.Context, Request) (Response, error) {
	i := int(s.calls.Add(1)) - 1
	if i < len(s.errs) && s.errs[i] != nil {
		return Response{}, s.errs[i]
	}
	return Response{Message: Message{Role: RoleAssistant, Text: "from " + s.name}, Stop: StopEnd}, nil
}

var busy = &APIError{Status: 503, Message: "high demand"}

func TestRetryRecoversFromTransientError(t *testing.T) {
	c := &scripted{name: "a", errs: []error{busy, busy}}
	resp, err := WithRetry(c, 3, time.Millisecond).Complete(context.Background(), Request{})
	if err != nil || resp.Message.Text != "from a" || c.calls.Load() != 3 {
		t.Fatalf("expected success on 3rd try, got %v after %d calls", err, c.calls.Load())
	}
	bad := &scripted{name: "b", errs: []error{&APIError{Status: 400, Message: "bad"}}}
	if _, err := WithRetry(bad, 3, time.Millisecond).Complete(context.Background(), Request{}); err == nil || bad.calls.Load() != 1 {
		t.Fatalf("400 must not be retried (calls=%d)", bad.calls.Load())
	}
}

func TestRouterFallsBackAndCoolsDown(t *testing.T) {
	primary := &scripted{name: "primary", errs: []error{busy}} // one spike, then healthy
	backup := &scripted{name: "backup"}
	r := NewRouter(time.Minute, Route{"primary", primary}, Route{"backup", backup})

	resp, err := r.Complete(context.Background(), Request{})
	if err != nil || resp.Message.Text != "from backup" {
		t.Fatalf("expected fallback to backup, got %+v %v", resp, err)
	}
	// Primary is cooling down: next request goes straight to backup.
	before := primary.calls.Load()
	if resp, _ := r.Complete(context.Background(), Request{}); resp.Message.Text != "from backup" || primary.calls.Load() != before {
		t.Fatal("cooling model should be skipped")
	}
	// After the cooldown, primary is preferred again.
	r.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	if resp, _ := r.Complete(context.Background(), Request{}); resp.Message.Text != "from primary" {
		t.Fatalf("expected primary after cooldown, got %q", resp.Message.Text)
	}
}

func TestRouterAllBusyAndRealErrors(t *testing.T) {
	a := &scripted{name: "a", errs: []error{busy, busy}}
	b := &scripted{name: "b", errs: []error{busy, busy}}
	_, err := NewRouter(time.Minute, Route{"a", a}, Route{"b", b}).Complete(context.Background(), Request{})
	if !errors.Is(err, ErrBusy) || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("expected friendly busy error, got %v", err)
	}
	bad := &scripted{name: "bad", errs: []error{&APIError{Status: 401, Message: "bad key"}}}
	never := &scripted{name: "never"}
	_, err = NewRouter(time.Minute, Route{"bad", bad}, Route{"never", never}).Complete(context.Background(), Request{})
	var api *APIError
	if !errors.As(err, &api) || api.Status != 401 || never.calls.Load() != 0 {
		t.Fatalf("a real error must surface, not fall back: %v (fallback calls=%d)", err, never.calls.Load())
	}
}

func TestNativeReplayedOnlyToSameModel(t *testing.T) {
	raw := json.RawMessage(`{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"x","arguments":"{}"},"extra_content":{"google":{"thought_signature":"SIG"}}}]}`)
	m := Message{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "c1", Name: "x", Input: json.RawMessage(`{}`)}}, Native: &Native{Kind: "openai", Model: "model-a", Raw: raw}}

	same, _ := json.Marshal(NewOpenAI("", "model-a", "").toWire(m))
	other, _ := json.Marshal(NewOpenAI("", "model-b", "").toWire(m))
	if !strings.Contains(string(same), "thought_signature") {
		t.Fatal("same model should get the native message")
	}
	if strings.Contains(string(other), "thought_signature") || !strings.Contains(string(other), `"name":"x"`) {
		t.Fatalf("a different model should get the neutral form: %s", other)
	}
}

func TestNewBuildsRouterWithFallbacks(t *testing.T) {
	c, err := New(Config{Provider: "openai", APIKey: "k", Model: "m1", Fallbacks: []string{"m2", "m1", " ", "m3"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(c.(*Router).Names(), ","); got != "m1,m2,m3" {
		t.Fatalf("routes = %s", got)
	}
}

type hanging struct{}

func (hanging) Complete(ctx context.Context, _ Request) (Response, error) {
	<-ctx.Done()
	return Response{}, ctx.Err()
}

func TestHungModelFallsBackFast(t *testing.T) {
	slow := &retrying{next: hanging{}, attempts: 3, base: time.Millisecond, timeout: 20 * time.Millisecond}
	backup := &scripted{name: "backup"}
	start := time.Now()
	resp, err := NewRouter(time.Minute, Route{"slow", slow}, Route{"backup", backup}).Complete(context.Background(), Request{})
	if err != nil || resp.Message.Text != "from backup" {
		t.Fatalf("expected fallback after timeout, got %v", err)
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Fatalf("a hung model must not be retried before falling back (took %s)", time.Since(start))
	}
}
