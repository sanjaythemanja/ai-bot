package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenAI speaks the Chat Completions protocol, which most hosted and local
// model servers implement. It uses plain HTTP so any base URL works.
type OpenAI struct {
	apiKey  string
	model   string
	baseURL string
	http    *http.Client
}

// NewOpenAI returns a client. An empty apiKey sends no Authorization header
// (for local servers such as Ollama).
func NewOpenAI(apiKey, model, baseURL string) *OpenAI {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return &OpenAI{apiKey: apiKey, model: model, baseURL: strings.TrimRight(baseURL, "/"),
		http: &http.Client{Timeout: 5 * time.Minute}}
}

type oaMessage struct {
	Role       string       `json:"role"`
	Content    *string      `json:"content"`
	ToolCalls  []oaToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
	Refusal    *string      `json:"refusal,omitempty"`
}

type oaToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type oaTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

type oaRequest struct {
	Model    string   `json:"model"`
	Messages []any    `json:"messages"` // oaMessage, or a replayed raw assistant message
	Tools    []oaTool `json:"tools,omitempty"`
}

type oaResponse struct {
	Choices []struct {
		Message      json.RawMessage `json:"message"`
		FinishReason string          `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func strPtr(s string) *string { return &s }

func (c *OpenAI) Complete(ctx context.Context, req Request) (Response, error) {
	body := oaRequest{Model: c.model}
	if req.System != "" {
		body.Messages = append(body.Messages, oaMessage{Role: "system", Content: strPtr(req.System)})
	}
	for _, m := range req.Messages {
		body.Messages = append(body.Messages, c.toWire(m)...)
	}
	for _, t := range req.Tools {
		var ot oaTool
		ot.Type = "function"
		ot.Function.Name, ot.Function.Description, ot.Function.Parameters = t.Name, t.Description, t.Schema()
		body.Tools = append(body.Tools, ot)
	}

	b, err := json.Marshal(body)
	if err != nil {
		return Response{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return Response{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	res, err := c.http.Do(httpReq)
	if err != nil {
		return Response{}, fmt.Errorf("llm request: %w", err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return Response{}, err
	}

	var out oaResponse
	_ = json.Unmarshal(raw, &out)
	if res.StatusCode/100 != 2 {
		msg := strings.TrimSpace(string(raw))
		if out.Error != nil && out.Error.Message != "" {
			msg = out.Error.Message
		}
		return Response{}, &APIError{Status: res.StatusCode, Message: truncate(msg, 300)}
	}
	if len(out.Choices) == 0 {
		return Response{}, fmt.Errorf("llm API returned no choices")
	}
	choice := out.Choices[0]
	var wm oaMessage
	if err := json.Unmarshal(choice.Message, &wm); err != nil {
		return Response{}, fmt.Errorf("llm API returned a malformed message: %w", err)
	}

	// Native keeps the raw message so provider-specific fields (for example
	// Gemini's thought signatures on tool calls) are sent back untouched.
	msg := Message{Role: RoleAssistant, Native: &Native{Kind: "openai", Model: c.model, Raw: choice.Message}}
	if wm.Content != nil {
		msg.Text = *wm.Content
	}
	for _, tc := range wm.ToolCalls {
		args := tc.Function.Arguments
		if strings.TrimSpace(args) == "" {
			args = "{}"
		}
		msg.ToolCalls = append(msg.ToolCalls, ToolCall{ID: tc.ID, Name: tc.Function.Name, Input: json.RawMessage(args)})
	}

	stop := StopEnd
	switch {
	case wm.Refusal != nil && *wm.Refusal != "":
		msg.Text, stop = *wm.Refusal, StopRefusal
	case choice.FinishReason == "content_filter":
		stop = StopRefusal
	case choice.FinishReason == "length":
		stop = StopMaxTokens
	case len(msg.ToolCalls) > 0:
		stop = StopToolUse
	}
	return Response{Message: msg, Stop: stop}, nil
}

// toWire converts a neutral message; tool results become one "tool" message each.
func (c *OpenAI) toWire(m Message) []any {
	if m.Native.replayable("openai", c.model) {
		return []any{m.Native.Raw}
	}
	var out []any
	switch m.Role {
	case RoleAssistant:
		wm := oaMessage{Role: "assistant"}
		if m.Text != "" || len(m.ToolCalls) == 0 {
			wm.Content = strPtr(m.Text)
		}
		for _, tc := range m.ToolCalls {
			var w oaToolCall
			w.ID, w.Type = tc.ID, "function"
			w.Function.Name, w.Function.Arguments = tc.Name, string(tc.Input)
			wm.ToolCalls = append(wm.ToolCalls, w)
		}
		out = append(out, wm)
	default:
		for _, r := range m.ToolResults {
			content := r.Content
			if r.IsError {
				content = "ERROR: " + content
			}
			out = append(out, oaMessage{Role: "tool", ToolCallID: r.CallID, Content: strPtr(content)})
		}
		if m.Text != "" {
			out = append(out, oaMessage{Role: "user", Content: strPtr(m.Text)})
		}
	}
	return out
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
