// Package llm is a provider-neutral chat + tool-calling interface. The agent
// only talks to Client; backends translate to a vendor's wire format.
//
// Backends:
//   - "openai": any OpenAI-compatible Chat Completions API (OpenAI/ChatGPT,
//     Azure-compatible gateways, OpenRouter, Groq, Together, Mistral,
//     DeepSeek, Ollama, LM Studio, vLLM, …) — set LLM_BASE_URL to switch.
//   - "anthropic": Claude via the official Anthropic SDK.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Role of a message author.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// ToolCall is a model's request to run a tool.
type ToolCall struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// ToolResult answers one ToolCall.
type ToolResult struct {
	CallID  string `json:"call_id"`
	Content string `json:"content"`
	IsError bool   `json:"is_error"`
}

// Message is one conversation turn. It is plain JSON so conversations can be
// stored and resumed later.
type Message struct {
	Role        Role         `json:"role"`
	Text        string       `json:"text,omitempty"`
	ToolCalls   []ToolCall   `json:"tool_calls,omitempty"`   // assistant turns
	ToolResults []ToolResult `json:"tool_results,omitempty"` // user turns answering tool calls
	// Native is the backend's own copy of an assistant turn. The same backend
	// replays it verbatim (keeping fields like reasoning blocks intact); other
	// backends ignore it and use the neutral fields above.
	Native *Native `json:"native,omitempty"`
}

// Native is a backend's raw wire form of a message, tagged by backend.
type Native struct {
	Kind string          `json:"kind"` // "openai" | "anthropic"
	Raw  json.RawMessage `json:"raw"`
}

// ToolSpec describes a tool as a JSON Schema object.
type ToolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Properties  map[string]any `json:"-"`
	Required    []string       `json:"-"`
}

// Schema returns the tool's input as a JSON Schema object.
func (t ToolSpec) Schema() map[string]any {
	props := t.Properties
	if props == nil {
		props = map[string]any{}
	}
	s := map[string]any{"type": "object", "properties": props}
	if len(t.Required) > 0 {
		s["required"] = t.Required
	}
	return s
}

// StopReason says why the model stopped.
type StopReason string

const (
	StopEnd       StopReason = "end"
	StopToolUse   StopReason = "tool_use"
	StopMaxTokens StopReason = "max_tokens"
	StopRefusal   StopReason = "refusal"
)

// Request is one model call.
type Request struct {
	System   string
	Messages []Message
	Tools    []ToolSpec
}

// Response is the model's reply.
type Response struct {
	Message Message
	Stop    StopReason
}

// Client is implemented by every backend.
type Client interface {
	Complete(ctx context.Context, req Request) (Response, error)
}

// Config selects and configures a backend.
type Config struct {
	Provider string // "openai" | "anthropic"; empty = auto-detect from key
	APIKey   string
	Model    string
	BaseURL  string // optional override
}

// Resolve fills Provider from the key prefix when it is not set.
func (c Config) Resolve() Config {
	if c.Provider == "" {
		if strings.HasPrefix(c.APIKey, "sk-ant-") {
			c.Provider = "anthropic"
		} else {
			c.Provider = "openai"
		}
	}
	c.Provider = strings.ToLower(c.Provider)
	if c.Model == "" && c.Provider == "anthropic" {
		c.Model = "claude-opus-5-5"
	}
	return c
}

// ErrNotConfigured means no usable model was configured.
var ErrNotConfigured = errors.New("no AI model configured: set LLM_API_KEY and LLM_MODEL")

// New builds the configured backend.
func New(c Config) (Client, error) {
	c = c.Resolve()
	switch c.Provider {
	case "openai", "openai-compatible":
		if c.Model == "" {
			return nil, fmt.Errorf("LLM_MODEL is required for the openai provider")
		}
		return NewOpenAI(c.APIKey, c.Model, c.BaseURL), nil
	case "anthropic", "claude":
		return NewAnthropic(c.APIKey, c.Model, c.BaseURL), nil
	}
	return nil, fmt.Errorf("unknown LLM_PROVIDER %q (use openai or anthropic)", c.Provider)
}
