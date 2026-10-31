package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

// A stored conversation must replay each provider's native message verbatim,
// including fields the neutral form drops (e.g. Gemini thought signatures).
func TestMessageJSONRoundTripKeepsNative(t *testing.T) {
	raw := json.RawMessage(`{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"x","arguments":"{}"},"extra_content":{"google":{"thought_signature":"SIG"}}}]}`)
	in := Message{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "c1", Name: "x", Input: json.RawMessage(`{}`)}}, Native: &Native{Kind: "openai", Raw: raw}}

	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Message
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	wire, _ := json.Marshal(NewOpenAI("", "m", "").toWire(out))
	if !strings.Contains(string(wire), "thought_signature") {
		t.Fatalf("native fields lost after round trip: %s", wire)
	}
}
