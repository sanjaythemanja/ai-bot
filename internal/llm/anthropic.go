package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// Anthropic calls Claude through the official SDK.
type Anthropic struct {
	client anthropic.Client
	model  string
}

// NewAnthropic returns a Claude client. extra options are for tests.
func NewAnthropic(apiKey, model, baseURL string, extra ...option.RequestOption) *Anthropic {
	opts := []option.RequestOption{option.WithRequestTimeout(5 * time.Minute)}
	if apiKey != "" {
		opts = append(opts, option.WithAPIKey(apiKey))
	}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	opts = append(opts, extra...)
	return &Anthropic{client: anthropic.NewClient(opts...), model: model}
}

func (a *Anthropic) Complete(ctx context.Context, req Request) (Response, error) {
	msgs := make([]anthropic.MessageParam, 0, len(req.Messages))
	for _, m := range req.Messages {
		msgs = append(msgs, a.toParam(m))
	}
	tools := make([]anthropic.ToolUnionParam, 0, len(req.Tools))
	for _, t := range req.Tools {
		props := t.Properties
		if props == nil {
			props = map[string]any{}
		}
		tp := anthropic.ToolParam{
			Name:        t.Name,
			Description: anthropic.String(t.Description),
			InputSchema: anthropic.ToolInputSchemaParam{Properties: props, Required: t.Required},
		}
		tools = append(tools, anthropic.ToolUnionParam{OfTool: &tp})
	}

	resp, err := a.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(a.model),
		MaxTokens: 16000,
		System:    []anthropic.TextBlockParam{{Text: req.System, CacheControl: anthropic.NewCacheControlEphemeralParam()}},
		Tools:     tools,
		Messages:  msgs,
	})
	if err != nil {
		return Response{}, describeAnthropicError(err)
	}

	// Keep the full assistant turn (including thinking blocks) for replay.
	out := Message{Role: RoleAssistant, Native: &Native{Kind: "anthropic", Raw: json.RawMessage(resp.RawJSON())}}
	var text []string
	for _, block := range resp.Content {
		switch b := block.AsAny().(type) {
		case anthropic.TextBlock:
			text = append(text, b.Text)
		case anthropic.ToolUseBlock:
			out.ToolCalls = append(out.ToolCalls, ToolCall{ID: b.ID, Name: b.Name, Input: json.RawMessage(b.JSON.Input.Raw())})
		}
	}
	out.Text = strings.TrimSpace(strings.Join(text, "\n\n"))

	stop := StopEnd
	switch resp.StopReason {
	case anthropic.StopReasonToolUse:
		stop = StopToolUse
	case anthropic.StopReasonRefusal:
		stop = StopRefusal
	case anthropic.StopReasonMaxTokens:
		stop = StopMaxTokens
	}
	return Response{Message: out, Stop: stop}, nil
}

func (a *Anthropic) toParam(m Message) anthropic.MessageParam {
	if m.Native != nil && m.Native.Kind == "anthropic" {
		var orig anthropic.Message
		if err := json.Unmarshal(m.Native.Raw, &orig); err == nil {
			return orig.ToParam()
		}
	}
	var blocks []anthropic.ContentBlockParamUnion
	if m.Role == RoleAssistant {
		if m.Text != "" {
			blocks = append(blocks, anthropic.NewTextBlock(m.Text))
		}
		for _, tc := range m.ToolCalls {
			blocks = append(blocks, anthropic.NewToolUseBlock(tc.ID, tc.Input, tc.Name))
		}
		return anthropic.NewAssistantMessage(blocks...)
	}
	for _, r := range m.ToolResults {
		blocks = append(blocks, anthropic.NewToolResultBlock(r.CallID, r.Content, r.IsError))
	}
	if m.Text != "" {
		blocks = append(blocks, anthropic.NewTextBlock(m.Text))
	}
	return anthropic.NewUserMessage(blocks...)
}

func describeAnthropicError(err error) error {
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.StatusCode == 401:
			return fmt.Errorf("Anthropic rejected the API key (401): %w", err)
		case apiErr.StatusCode == 429:
			return fmt.Errorf("Anthropic rate limit hit; try again shortly: %w", err)
		case apiErr.StatusCode >= 500:
			return fmt.Errorf("Anthropic API error %d; try again: %w", apiErr.StatusCode, err)
		}
	}
	return fmt.Errorf("anthropic: %w", err)
}
