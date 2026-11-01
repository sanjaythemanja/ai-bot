package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/sanjayrj/ai-bot/internal/llm"
	"github.com/sanjayrj/ai-bot/internal/plugin"
	"github.com/sanjayrj/ai-bot/internal/provider"
	"github.com/sanjayrj/ai-bot/internal/store"
)

// Toolbox is the plugin engine. It knows nothing about any integration: it
// collects tools from plugins, runs reads, turns writes into Actions (which
// wait for approval unless requireApproval is off), and routes approved
// actions to the plugin that executes their kind.
type Toolbox struct {
	actions         store.Actions
	requireApproval bool

	plugins   []plugin.Plugin
	tools     map[string]plugin.Tool
	order     []string
	executors map[string]plugin.Executor // by action kind
	editors   map[string]plugin.Editor   // by action kind
	dedupers  map[string]plugin.Deduper  // by action kind
}

// NewToolbox registers plugins. Tool names and action kinds must be unique.
func NewToolbox(actions store.Actions, requireApproval bool, plugins ...plugin.Plugin) *Toolbox {
	t := &Toolbox{
		actions: actions, requireApproval: requireApproval,
		tools:     map[string]plugin.Tool{},
		executors: map[string]plugin.Executor{},
		editors:   map[string]plugin.Editor{},
		dedupers:  map[string]plugin.Deduper{},
	}
	for _, p := range plugins {
		t.register(p)
	}
	for _, tl := range t.coreTools() {
		t.addTool(tl)
	}
	return t
}

func (t *Toolbox) register(p plugin.Plugin) {
	t.plugins = append(t.plugins, p)
	for _, tl := range p.Tools() {
		t.addTool(tl)
	}
	if ex, ok := p.(plugin.Executor); ok {
		for _, k := range ex.Kinds() {
			if _, dup := t.executors[k]; dup {
				panic(fmt.Sprintf("action kind %q registered twice", k))
			}
			t.executors[k] = ex
			if ed, ok := p.(plugin.Editor); ok {
				t.editors[k] = ed
			}
			if dd, ok := p.(plugin.Deduper); ok {
				t.dedupers[k] = dd
			}
		}
	}
}

func (t *Toolbox) addTool(tl plugin.Tool) {
	if _, dup := t.tools[tl.Name]; dup {
		panic(fmt.Sprintf("tool %q registered twice", tl.Name))
	}
	if tl.Write {
		tl.Description = t.writeDesc(tl.Description)
	}
	t.tools[tl.Name] = tl
	t.order = append(t.order, tl.Name)
}

// ErrUnknownTool is returned for a tool name that is not registered.
var ErrUnknownTool = errors.New("unknown tool")

// Specs returns the tool list in a stable order (keeps prompts cacheable).
func (t *Toolbox) Specs() []llm.ToolSpec {
	out := make([]llm.ToolSpec, 0, len(t.order))
	for _, name := range t.order {
		tl := t.tools[name]
		out = append(out, llm.ToolSpec{Name: tl.Name, Description: tl.Description, Properties: tl.Properties, Required: tl.Required})
	}
	return out
}

// Call runs a tool and returns its JSON-encoded result.
func (t *Toolbox) Call(ctx context.Context, name string, input json.RawMessage) (string, error) {
	tl, ok := t.tools[name]
	if !ok {
		return "", fmt.Errorf("%w %q", ErrUnknownTool, name)
	}
	var res any
	var err error
	if tl.Write {
		var p plugin.Proposal
		if p, err = tl.Propose(ctx, input); err == nil {
			res, err = t.queue(ctx, p)
		}
	} else {
		res, err = tl.Run(ctx, input)
	}
	if err != nil {
		if errors.Is(err, provider.ErrReauth) {
			return "", provider.ErrReauth // clean, actionable message for the model and user
		}
		if errors.Is(err, provider.ErrNotConnected) {
			return "", fmt.Errorf("%w: ask the user to connect it", err)
		}
		return "", err
	}
	b, err := json.Marshal(res)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Serial reports whether a tool changes state (a write or a mutating tool),
// so it must not run alongside other tool calls.
func (t *Toolbox) Serial(name string) bool {
	tl, ok := t.tools[name]
	return ok && (tl.Write || tl.Mutates)
}

// RequiresApproval reports whether writes wait for the user.
func (t *Toolbox) RequiresApproval() bool { return t.requireApproval }

// NeedsApproval reports whether the named tool will wait for the user.
func (t *Toolbox) NeedsApproval(name string) bool {
	return t.requireApproval && t.tools[name].Write
}

// Guidance collects plugin-specific instructions for the system prompt.
func (t *Toolbox) Guidance() string {
	var parts []string
	for _, p := range t.plugins {
		if g, ok := p.(plugin.Guide); ok {
			if s := strings.TrimSpace(g.Guidance()); s != "" {
				parts = append(parts, s)
			}
		}
	}
	return strings.Join(parts, "\n")
}

// Sorter returns the first plugin that can rank emails, if any.
func (t *Toolbox) Sorter() plugin.Sorter {
	for _, p := range t.plugins {
		if s, ok := p.(plugin.Sorter); ok {
			return s
		}
	}
	return nil
}

// Plugins lists registered plugin names (for status displays).
func (t *Toolbox) Plugins() []string {
	out := make([]string, 0, len(t.plugins))
	for _, p := range t.plugins {
		out = append(out, p.Name())
	}
	sort.Strings(out)
	return out
}

func (t *Toolbox) writeDesc(what string) string {
	if t.requireApproval {
		return what + " It is NOT done immediately: it is queued for the user's approval in the 'Waiting for you' approvals list."
	}
	return what + " This runs immediately and cannot be undone, so only call it once the user has clearly asked for it."
}

// queue stores a proposal as an Action and, in auto mode, runs it at once.
func (t *Toolbox) queue(ctx context.Context, p plugin.Proposal) (any, error) {
	if _, ok := t.executors[p.Kind]; !ok {
		return nil, fmt.Errorf("no plugin can execute %q", p.Kind)
	}
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	if t.requireApproval {
		if dup, ok := t.duplicateOf(ctx, uid, p); ok {
			return nil, fmt.Errorf("a matching action is already waiting for approval (action_id %s: %s). "+
				"To change it, call edit_pending_action with action_id %s. "+
				"If it should be replaced entirely, call cancel_pending_action on it first", dup.ID, dup.Summary, dup.ID)
		}
	}
	a, err := t.actions.Add(ctx, uid, p.Kind, p.Summary, reasonFrom(ctx), p.Payload)
	if err != nil {
		return nil, err
	}
	if t.requireApproval {
		return map[string]string{
			"status":    "awaiting_user_approval",
			"action_id": a.ID,
			"summary":   p.Summary,
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
	return map[string]string{"status": "done", "action_id": a.ID, "summary": p.Summary, "result": a.Result}, nil
}

func (t *Toolbox) duplicateOf(ctx context.Context, uid string, p plugin.Proposal) (Action, bool) {
	dd, ok := t.dedupers[p.Kind]
	if !ok {
		return Action{}, false
	}
	next, err := json.Marshal(p.Payload)
	if err != nil {
		return Action{}, false
	}
	pending, err := t.actions.List(ctx, uid, 100)
	if err != nil {
		return Action{}, false
	}
	for _, a := range pending {
		if a.Status == StatusPending && a.Kind == p.Kind && dd.Duplicate(p.Kind, a.Payload, next) {
			return a, true
		}
	}
	return Action{}, false
}

// Approve claims a pending action and executes it exactly once via the
// plugin that owns its kind.
func (t *Toolbox) Approve(ctx context.Context, id string) (Action, error) {
	uid, err := userID(ctx)
	if err != nil {
		return Action{}, err
	}
	a, err := t.actions.Claim(ctx, uid, id, StatusExecuted)
	if err != nil {
		return a, err
	}
	ex, ok := t.executors[a.Kind]
	if !ok {
		return t.actions.Finish(ctx, uid, id, StatusFailed, "", fmt.Sprintf("no plugin can execute %q", a.Kind))
	}
	result, err := ex.Execute(ctx, a.Kind, a.Payload)
	if err != nil {
		return t.actions.Finish(ctx, uid, id, StatusFailed, "", err.Error())
	}
	return t.actions.Finish(ctx, uid, id, StatusExecuted, result, "")
}
