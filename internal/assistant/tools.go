package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/sanjayrj/ai-bot/internal/llm"
	"github.com/sanjayrj/ai-bot/internal/provider"
	"github.com/sanjayrj/ai-bot/internal/store"
)

// tool couples a model-neutral tool definition with its Go implementation.
type tool struct {
	def llm.ToolSpec
	run func(ctx context.Context, input json.RawMessage) (any, error)
}

// Toolbox exposes the integrations to the model. Reads run immediately;
// writes that leave the user's account become Actions, which either wait for
// approval or (with requireApproval off) run at once and are kept as a log.
//
// Tools are registered in tools_mail.go, tools_calendar.go and
// tools_actions.go; writes are carried out in execute.go.
type Toolbox struct {
	mail            provider.Mail
	cal             provider.Calendar
	meet            provider.Meetings
	actions         store.Actions
	loc             *time.Location
	requireApproval bool
	tools           map[string]tool
	order           []string
}

func NewToolbox(mail provider.Mail, cal provider.Calendar, meet provider.Meetings, actions store.Actions, loc *time.Location, requireApproval bool) *Toolbox {
	t := &Toolbox{mail: mail, cal: cal, meet: meet, actions: actions, loc: loc, requireApproval: requireApproval, tools: map[string]tool{}}
	t.registerMail()
	t.registerCalendar()
	t.registerPeople()
	t.registerActions()
	return t
}

// ErrUnknownTool is returned for a tool name that is not registered.
var ErrUnknownTool = errors.New("unknown tool")

// writeTools are the tools that change something outside the app.
var writeTools = map[string]bool{
	"send_email": true, "send_draft": true, "delete_draft": true,
	"create_event": true, "update_event": true, "delete_event": true,
}

// Specs returns the tool list in a stable order (keeps prompts cacheable).
func (t *Toolbox) Specs() []llm.ToolSpec {
	out := make([]llm.ToolSpec, 0, len(t.order))
	for _, name := range t.order {
		out = append(out, t.tools[name].def)
	}
	return out
}

// Call runs a tool and returns its JSON-encoded result.
func (t *Toolbox) Call(ctx context.Context, name string, input json.RawMessage) (string, error) {
	tl, ok := t.tools[name]
	if !ok {
		return "", fmt.Errorf("%w %q", ErrUnknownTool, name)
	}
	res, err := tl.run(ctx, input)
	if err != nil {
		if errors.Is(err, provider.ErrNotConnected) {
			return "", fmt.Errorf("%w: ask the user to connect it from the sidebar", err)
		}
		return "", err
	}
	b, err := json.Marshal(res)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// RequiresApproval reports whether writes wait for the user.
func (t *Toolbox) RequiresApproval() bool { return t.requireApproval }

// NeedsApproval reports whether the named tool will wait for the user.
func (t *Toolbox) NeedsApproval(name string) bool { return t.requireApproval && writeTools[name] }

type prop = map[string]any

func (t *Toolbox) add(name, desc string, props prop, required []string, run func(context.Context, json.RawMessage) (any, error)) {
	t.tools[name] = tool{
		def: llm.ToolSpec{Name: name, Description: desc, Properties: props, Required: required},
		run: run,
	}
	t.order = append(t.order, name)
}

func str(d string) prop { return prop{"type": "string", "description": d} }
func num(d string) prop { return prop{"type": "integer", "description": d} }
func strs(d string) prop {
	return prop{"type": "array", "items": prop{"type": "string"}, "description": d}
}

func merge(ps ...prop) prop {
	out := prop{}
	for _, p := range ps {
		for k, v := range p {
			out[k] = v
		}
	}
	return out
}

func decode[T any](raw json.RawMessage) (T, error) {
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, fmt.Errorf("invalid tool input: %w", err)
	}
	return v, nil
}

func (t *Toolbox) parseTime(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"} {
		if v, err := time.ParseInLocation(layout, s, t.loc); err == nil {
			return v, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse time %q; use RFC 3339 like 2026-01-02T15:04:05-07:00", s)
}

func (t *Toolbox) fmtTime(v time.Time) string { return v.In(t.loc).Format("Mon Jan 2 15:04") }

func (t *Toolbox) writeDesc(what string) string {
	if t.requireApproval {
		return what + " It is NOT done immediately: it is queued for the user's approval in the 'Waiting for you' approvals list."
	}
	return what + " This runs immediately and cannot be undone, so only call it once the user has clearly asked for it."
}

func (t *Toolbox) queue(ctx context.Context, kind ActionKind, summary string, payload any) (any, error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	if t.requireApproval {
		if dup, ok := t.duplicateOf(ctx, uid, kind, payload); ok {
			return nil, duplicateError(dup)
		}
	}
	a, err := t.actions.Add(ctx, uid, kind, summary, payload)
	if err != nil {
		return nil, err
	}
	if t.requireApproval {
		return map[string]string{
			"status":    "awaiting_user_approval",
			"action_id": a.ID,
			"summary":   summary,
			"note":      "Queued. Tell the user it is waiting in the 'Waiting for you' approvals list; do not claim it was done. Use edit_pending_action to change it before approval.",
		}, nil
	}
	// Auto mode: run now, but keep the action as an audit log entry.
	a, err = t.Approve(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	if a.Status == StatusFailed {
		return nil, errors.New(a.Error)
	}
	return map[string]string{"status": "done", "action_id": a.ID, "summary": summary, "result": a.Result}, nil
}

// Approve claims a pending action and executes it exactly once.
func (t *Toolbox) Approve(ctx context.Context, id string) (Action, error) {
	uid, err := userID(ctx)
	if err != nil {
		return Action{}, err
	}
	a, err := t.actions.Claim(ctx, uid, id, StatusExecuted)
	if err != nil {
		return a, err
	}
	result, err := t.execute(ctx, a)
	if err != nil {
		return t.actions.Finish(ctx, uid, id, StatusFailed, "", err.Error())
	}
	return t.actions.Finish(ctx, uid, id, StatusExecuted, result, "")
}

type slot struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

func (t *Toolbox) freeSlots(ctx context.Context, date string, minutes int, dayStart, dayEnd string) ([]slot, error) {
	if minutes <= 0 {
		minutes = 30
	}
	from, to, err := t.dayWindow(date, dayStart, dayEnd)
	if err != nil {
		return nil, err
	}
	events, err := t.cal.ListEvents(ctx, "", from, to)
	if err != nil {
		return nil, err
	}
	var busy []provider.Busy
	for _, e := range events {
		if !e.AllDay {
			busy = append(busy, provider.Busy{Start: e.Start, End: e.End})
		}
	}
	return gaps(from, to, busy, time.Duration(minutes)*time.Minute), nil
}

// dayWindow parses a date plus HH:MM bounds in the user's time zone.
func (t *Toolbox) dayWindow(date, dayStart, dayEnd string) (time.Time, time.Time, error) {
	if dayStart == "" {
		dayStart = "09:00"
	}
	if dayEnd == "" {
		dayEnd = "18:00"
	}
	from, err := time.ParseInLocation("2006-01-02 15:04", date+" "+dayStart, t.loc)
	if err != nil {
		return from, from, fmt.Errorf("bad date/day_start: %w", err)
	}
	to, err := time.ParseInLocation("2006-01-02 15:04", date+" "+dayEnd, t.loc)
	if err != nil {
		return from, to, fmt.Errorf("bad date/day_end: %w", err)
	}
	return from, to, nil
}

// gaps returns the free stretches of at least need between from and to,
// skipping time already past.
func gaps(from, to time.Time, busy []provider.Busy, need time.Duration) []slot {
	sort.Slice(busy, func(i, j int) bool { return busy[i].Start.Before(busy[j].Start) })
	out := []slot{}
	cursor := from
	if now := time.Now(); now.After(cursor) && now.Before(to) {
		cursor = now.Truncate(15 * time.Minute).Add(15 * time.Minute)
	}
	for _, b := range busy {
		if b.Start.Sub(cursor) >= need {
			out = append(out, slot{cursor, b.Start})
		}
		if b.End.After(cursor) {
			cursor = b.End
		}
	}
	if to.Sub(cursor) >= need {
		out = append(out, slot{cursor, to})
	}
	return out
}
