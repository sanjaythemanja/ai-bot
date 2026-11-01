// Package assistant is the core go-kit service: business logic (service.go),
// service middlewares (middleware.go), endpoints (endpoints.go) and the HTTP
// transport (transport_http.go).
package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sanjayrj/ai-bot/internal/llm"
	"github.com/sanjayrj/ai-bot/internal/plugin"
	"github.com/sanjayrj/ai-bot/internal/provider"
	"github.com/sanjayrj/ai-bot/internal/store"
)

// Service is the executive assistant's API.
type Service interface {
	Chat(ctx context.Context, sessionID, message string) (ChatReply, error)
	ListConversations(ctx context.Context) ([]store.Summary, error)
	GetConversation(ctx context.Context, id string) (store.Conversation, error)
	DeleteConversation(ctx context.Context, id string) error
	Briefing(ctx context.Context) (Briefing, error)
	Overview(ctx context.Context) (Overview, error)
	ListActions(ctx context.Context) ([]Action, error)
	ApproveAction(ctx context.Context, id string) (Action, error)
	RejectAction(ctx context.Context, id string) (Action, error)
	Status(ctx context.Context) (Status, error)

	// Tools and CallTool let any external agent use the integrations
	// directly. Writes follow the same approval setting as chat.
	Tools(ctx context.Context) ([]ToolInfo, error)
	CallTool(ctx context.Context, name string, input json.RawMessage) (ToolOutput, error)
}

// ChatReply is the assistant's answer to one user message.
type ChatReply struct {
	Reply     string     `json:"reply"`
	ToolCalls []ToolCall `json:"tool_calls"`
	// Warning is set when the reply was produced but could not be saved.
	Warning string `json:"warning,omitempty"`
}

// Briefing is an AI-written daily brief in Markdown.
type Briefing struct {
	Markdown    string     `json:"markdown"`
	GeneratedAt time.Time  `json:"generated_at"`
	ToolCalls   []ToolCall `json:"tool_calls"`
}

// Overview is the raw, non-AI snapshot shown on the dashboard.
type Overview struct {
	Emails []provider.Email `json:"emails"`
	Events []provider.Event `json:"events"`
	// Priority explains how important each email is, keyed by email ID.
	Priority    map[string]plugin.Verdict `json:"priority,omitempty"`
	Errors      map[string]string         `json:"errors,omitempty"`
	GeneratedAt time.Time                 `json:"generated_at"`
}

// Status reports which integrations are usable.
type Status struct {
	DemoMode         bool   `json:"demo_mode"`
	AIConfigured     bool   `json:"ai_configured"`
	AIProvider       string `json:"ai_provider"`
	Model            string `json:"model"`
	AIError          string `json:"ai_error,omitempty"`
	GoogleConfigured bool   `json:"google_configured"`
	Gmail            bool   `json:"gmail"`
	Calendar         bool   `json:"calendar"`
	Timezone         string `json:"timezone"`
	RequireApproval  bool   `json:"require_approval"`
	UndoSeconds      int    `json:"undo_seconds"`
	Storage          string `json:"storage"`
}

// ToolInfo describes a tool in the JSON Schema form most agent frameworks
// (OpenAI functions, Anthropic tools, MCP) accept.
type ToolInfo struct {
	Name          string         `json:"name"`
	Description   string         `json:"description"`
	InputSchema   map[string]any `json:"input_schema"`
	NeedsApproval bool           `json:"needs_approval"`
}

// ToolOutput is the result of a direct tool call.
type ToolOutput struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

var ErrEmptyMessage = errors.New("message is required")

// Config wires a service.
type Config struct {
	Mail             provider.Mail
	Calendar         provider.Calendar
	Agent            *Agent
	Tools            *Toolbox
	Actions          store.Actions
	Location         *time.Location
	DemoMode         bool
	AIConfigured     bool
	AIProvider       string
	AIError          string // why the model could not be configured, if it couldn't
	Model            string
	GoogleConfigured bool
	Store            store.Store // conversation history; nil means in-memory
	StoreKind        string      // "postgres" or "memory", for /api/status
	UndoSeconds      int         // undo window the UI waits before approving
}

type service struct {
	cfg Config

	mu    sync.Mutex
	locks map[string]*sync.Mutex // serializes turns within one conversation
}

// NewService returns the basic service with no middleware.
func NewService(cfg Config) Service {
	if cfg.Store == nil {
		cfg.Store, cfg.StoreKind = store.NewMemory(), "memory"
	}
	return &service{cfg: cfg, locks: map[string]*sync.Mutex{}}
}

func (s *service) lock(id string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.locks[id]
	if !ok {
		l = &sync.Mutex{}
		s.locks[id] = l
	}
	return l
}

func (s *service) aiReady() error {
	if s.cfg.AIConfigured {
		return nil
	}
	if s.cfg.AIError != "" {
		return fmt.Errorf("%w (%s)", llm.ErrNotConfigured, s.cfg.AIError)
	}
	return llm.ErrNotConfigured
}

func (s *service) Chat(ctx context.Context, sessionID, message string) (ChatReply, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return ChatReply{}, ErrEmptyMessage
	}
	if err := s.aiReady(); err != nil {
		return ChatReply{}, err
	}
	uid, err := userID(ctx)
	if err != nil {
		return ChatReply{}, err
	}
	if sessionID == "" {
		sessionID = "default-" + uid
	}
	l := s.lock(uid + "/" + sessionID)
	l.Lock()
	defer l.Unlock()

	prev, err := s.cfg.Store.History(ctx, uid, sessionID)
	if err != nil {
		return ChatReply{}, fmt.Errorf("loading conversation: %w", err)
	}
	history, reply, calls, runErr := s.cfg.Agent.Run(withReason(ctx, message), prev, message)

	// Save the turn (even a failed one, so the transcript shows it). Only the
	// messages this turn added are appended; a failed run adds none.
	turn := store.Turn{UserText: message, Reply: reply}
	if len(calls) > 0 {
		turn.ToolCalls, _ = json.Marshal(calls)
	}
	if runErr != nil {
		turn.Error = runErr.Error()
	}
	saveErr := s.cfg.Store.AppendTurn(ctx, uid, sessionID, history[len(prev):], turn)

	if runErr != nil {
		return ChatReply{ToolCalls: calls}, runErr
	}
	out := ChatReply{Reply: reply, ToolCalls: calls}
	if saveErr != nil {
		out.Warning = "reply not saved to history: " + saveErr.Error()
	}
	return out, nil
}

func (s *service) ListConversations(ctx context.Context) ([]store.Summary, error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	return s.cfg.Store.List(ctx, uid, 50)
}

func (s *service) GetConversation(ctx context.Context, id string) (store.Conversation, error) {
	uid, err := userID(ctx)
	if err != nil {
		return store.Conversation{}, err
	}
	return s.cfg.Store.Get(ctx, uid, id)
}

func (s *service) DeleteConversation(ctx context.Context, id string) error {
	uid, err := userID(ctx)
	if err != nil {
		return err
	}
	l := s.lock(uid + "/" + id)
	l.Lock()
	defer l.Unlock()
	return s.cfg.Store.Delete(ctx, uid, id)
}

const briefingPrompt = `Prepare my daily briefing. Check unread and recent inbox mail (last 2 days, ranked with triage_inbox if available), today's and tomorrow's calendar, and my follow-ups (find_my_commitments, find_slack_commitments and find_awaiting_replies, whichever exist).

Format it in Markdown with these sections:
## Top priorities — the 3–5 things that most need me today, each with why and a suggested next step
## Today's schedule — times, who, and any prep needed; flag conflicts or back-to-backs
## Needs a reply — emails waiting on me, with why each matters
## Follow-ups — promises I made that are due soon or overdue (say where: email or Slack), then emails that nobody has answered for 2+ days; offer to draft a nudge
## FYI — brief, skip noise

Do not queue any actions; just brief me.`

func (s *service) Briefing(ctx context.Context) (Briefing, error) {
	if err := s.aiReady(); err != nil {
		return Briefing{}, err
	}
	_, md, calls, err := s.cfg.Agent.Run(withReason(ctx, "Daily brief"), nil, briefingPrompt)
	if err != nil {
		return Briefing{ToolCalls: calls}, err
	}
	return Briefing{Markdown: md, GeneratedAt: time.Now(), ToolCalls: calls}, nil
}

func (s *service) Overview(ctx context.Context) (Overview, error) {
	now := time.Now().In(s.cfg.Location)
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.cfg.Location)

	ov := Overview{Errors: map[string]string{}, GeneratedAt: now}
	var mu sync.Mutex
	var wg sync.WaitGroup
	fail := func(key string, err error) {
		msg := err.Error()
		if errors.Is(err, provider.ErrReauth) {
			msg = "Your Google access expired. Sign out and sign in again."
		}
		mu.Lock()
		ov.Errors[key] = msg
		mu.Unlock()
	}
	wg.Add(2)
	go func() {
		defer wg.Done()
		v, err := s.cfg.Mail.ListMessages(ctx, "is:unread in:inbox newer_than:3d", 10)
		if err != nil {
			fail("gmail", err)
		}
		ov.Emails = v
		if sorter := s.cfg.Tools.Sorter(); sorter != nil && len(v) > 0 {
			ov.Priority, _ = sorter.SortEmails(ctx, v)
		}
	}()
	go func() {
		defer wg.Done()
		v, err := s.cfg.Calendar.ListEvents(ctx, "", dayStart, dayStart.AddDate(0, 0, 1))
		if err != nil {
			fail("calendar", err)
		}
		ov.Events = v
	}()
	wg.Wait()
	if len(ov.Errors) == 0 {
		ov.Errors = nil
	}
	return ov, nil
}

func (s *service) ListActions(ctx context.Context) ([]Action, error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	return s.cfg.Actions.List(ctx, uid, 100)
}

func (s *service) RejectAction(ctx context.Context, id string) (Action, error) {
	uid, err := userID(ctx)
	if err != nil {
		return Action{}, err
	}
	return s.cfg.Actions.Claim(ctx, uid, id, StatusRejected)
}

// ApproveAction executes the queued write against the real integration.
func (s *service) ApproveAction(ctx context.Context, id string) (Action, error) {
	return s.cfg.Tools.Approve(ctx, id)
}

func (s *service) Status(context.Context) (Status, error) {
	return Status{
		DemoMode:         s.cfg.DemoMode,
		AIConfigured:     s.cfg.AIConfigured,
		AIProvider:       s.cfg.AIProvider,
		Model:            s.cfg.Model,
		AIError:          s.cfg.AIError,
		GoogleConfigured: s.cfg.GoogleConfigured,
		Gmail:            s.cfg.Mail.Connected(),
		Calendar:         s.cfg.Calendar.Connected(),
		Timezone:         zoneName(s.cfg.Location),
		RequireApproval:  s.cfg.Tools.RequiresApproval(),
		UndoSeconds:      s.cfg.UndoSeconds,
		Storage:          s.cfg.StoreKind,
	}, nil
}

func (s *service) Tools(context.Context) ([]ToolInfo, error) {
	specs := s.cfg.Tools.Specs()
	out := make([]ToolInfo, 0, len(specs))
	for _, t := range specs {
		out = append(out, ToolInfo{Name: t.Name, Description: t.Description, InputSchema: t.Schema(), NeedsApproval: s.cfg.Tools.NeedsApproval(t.Name)})
	}
	return out, nil
}

func (s *service) CallTool(ctx context.Context, name string, input json.RawMessage) (ToolOutput, error) {
	if len(input) == 0 {
		input = json.RawMessage("{}")
	}
	out, err := s.cfg.Tools.Call(ctx, name, input)
	if errors.Is(err, ErrUnknownTool) {
		return ToolOutput{}, err
	}
	if err != nil {
		// Tool failures are results, not transport errors, so agents can react.
		return ToolOutput{Error: err.Error()}, nil
	}
	return ToolOutput{Result: json.RawMessage(out)}, nil
}

func zoneName(loc *time.Location) string {
	if name := loc.String(); name != "Local" {
		return name
	}
	name, _ := time.Now().In(loc).Zone()
	return name
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
