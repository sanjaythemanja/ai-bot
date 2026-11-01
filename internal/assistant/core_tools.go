package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/sanjayrj/ai-bot/internal/plugin"
)

// coreTools are the engine's own tools; they work for every plugin's actions.
func (t *Toolbox) coreTools() []plugin.Tool {
	// edit_pending_action accepts the union of all editable fields; each
	// kind only accepts its own (checked in editPending).
	editProps := plugin.Props(map[string]any{"action_id": plugin.Str("Pending action ID")})
	var editable []string
	for kind, ed := range t.editors {
		if f := ed.EditFields(kind); f != nil {
			editProps = plugin.Props(editProps, f)
			names := make([]string, 0, len(f))
			for k := range f {
				names = append(names, k)
			}
			sort.Strings(names)
			editable = append(editable, fmt.Sprintf("%s: %s", kind, strings.Join(names, ", ")))
		}
	}
	sort.Strings(editable)

	return []plugin.Tool{
		{
			Name:        "list_pending_actions",
			Description: "List actions waiting for the user's approval, with their IDs and full details.",
			Properties:  map[string]any{},
			Run: func(ctx context.Context, _ json.RawMessage) (any, error) {
				uid, err := userID(ctx)
				if err != nil {
					return nil, err
				}
				all, err := t.actions.List(ctx, uid, 100)
				if err != nil {
					return nil, err
				}
				out := []Action{}
				for _, a := range all {
					if a.Status == StatusPending {
						out = append(out, a)
					}
				}
				return out, nil
			},
		},
		{
			Name:    "edit_pending_action",
			Mutates: true,
			Description: "Change an action that is still waiting for approval, instead of queuing a new one. " +
				"Only the fields you pass change. Editable fields by action kind — " + strings.Join(editable, "; ") + ".",
			Properties: editProps,
			Required:   []string{"action_id"},
			Run:        t.editPending,
		},
		{
			Name:        "cancel_pending_action",
			Mutates:     true,
			Description: "Cancel (reject) an action that is waiting for approval, so it never runs.",
			Properties:  map[string]any{"action_id": plugin.Str("Pending action ID")},
			Required:    []string{"action_id"},
			Run: func(ctx context.Context, raw json.RawMessage) (any, error) {
				in, err := plugin.Decode[struct {
					ID string `json:"action_id"`
				}](raw)
				if err != nil {
					return nil, err
				}
				uid, err := userID(ctx)
				if err != nil {
					return nil, err
				}
				a, err := t.actions.Claim(ctx, uid, in.ID, StatusRejected)
				if err != nil {
					return nil, err
				}
				return map[string]string{"status": "cancelled", "action_id": a.ID, "summary": a.Summary}, nil
			},
		},
	}
}

func (t *Toolbox) editPending(ctx context.Context, raw json.RawMessage) (any, error) {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return nil, fmt.Errorf("invalid tool input: %w", err)
	}
	var id string
	if err := json.Unmarshal(keys["action_id"], &id); err != nil || id == "" {
		return nil, fmt.Errorf("action_id is required")
	}
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	a, err := t.actions.Get(ctx, uid, id)
	if err != nil {
		return nil, err
	}
	if a.Status != StatusPending {
		return nil, fmt.Errorf("%w: action %s is %s", ErrActionResolved, id, a.Status)
	}

	ed, ok := t.editors[a.Kind]
	var allowed map[string]any
	if ok {
		allowed = ed.EditFields(a.Kind)
	}
	if allowed == nil {
		return nil, fmt.Errorf("a %s action has nothing to edit; cancel it and queue a new one instead", a.Kind)
	}
	var bad []string
	changes := map[string]json.RawMessage{}
	for k, v := range keys {
		if k == "action_id" {
			continue
		}
		if _, ok := allowed[k]; !ok {
			bad = append(bad, k)
			continue
		}
		changes[k] = v
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return nil, fmt.Errorf("field(s) %s do not apply to a %s action", strings.Join(bad, ", "), a.Kind)
	}
	cb, _ := json.Marshal(changes)
	p, err := ed.Edit(ctx, a.Kind, a.Payload, cb)
	if err != nil {
		return nil, err
	}
	a, err = t.actions.UpdatePending(ctx, uid, id, p.Summary, p.Payload)
	if err != nil {
		return nil, err
	}
	return map[string]any{"status": "pending_action_updated", "action_id": a.ID, "summary": a.Summary, "payload": a.Payload}, nil
}
