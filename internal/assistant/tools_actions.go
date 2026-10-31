package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/sanjayrj/ai-bot/internal/provider"
)

var (
	emailFields = fieldSet(emailChangeProps)
	eventFields = fieldSet(eventChangeProps)
)

func fieldSet(p prop) map[string]bool {
	out := map[string]bool{}
	for k := range p {
		out[k] = true
	}
	return out
}

func (t *Toolbox) registerActions() {
	t.add("list_pending_actions",
		"List actions waiting for the user's approval (emails to send, events to create/update/cancel), with their IDs and full details.",
		prop{}, nil,
		func(ctx context.Context, _ json.RawMessage) (any, error) {
			uid, err := userID(ctx)
			if err != nil {
				return nil, err
			}
			all, err := t.actions.List(ctx, uid, 100)
			if err != nil {
				return nil, err
			}
			var out []Action
			for _, a := range all {
				if a.Status == StatusPending {
					out = append(out, a)
				}
			}
			return out, nil
		})

	t.add("edit_pending_action",
		"Change an action that is still waiting for approval, instead of queuing a new one. "+
			"For a queued email you can change to/cc/bcc/subject/body (or add_to/add_cc/add_bcc/remove). "+
			"For a queued new event or event update you can change title/description/location/start/end/attendees (or add_attendees/remove_attendees). "+
			"Only the fields you pass change.",
		merge(prop{"action_id": str("Pending action ID")}, emailChangeProps, eventChangeProps), []string{"action_id"},
		func(ctx context.Context, raw json.RawMessage) (any, error) {
			return t.editPending(ctx, raw)
		})

	t.add("cancel_pending_action", "Cancel (reject) an action that is waiting for approval, so it never runs.",
		prop{"action_id": str("Pending action ID")}, []string{"action_id"},
		func(ctx context.Context, raw json.RawMessage) (any, error) {
			in, err := decode[struct {
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
		})
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

	allowed := map[ActionKind]map[string]bool{
		ActionSendEmail: emailFields, ActionCreateEvent: eventFields, ActionUpdateEvent: eventFields,
	}[a.Kind]
	if allowed == nil {
		return nil, fmt.Errorf("a %s action has nothing to edit; cancel it and queue a new one instead", a.Kind)
	}
	var bad []string
	for k := range keys {
		if k != "action_id" && !allowed[k] {
			bad = append(bad, k)
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return nil, fmt.Errorf("field(s) %s do not apply to a %s action", strings.Join(bad, ", "), a.Kind)
	}

	var summary string
	var payload any
	switch a.Kind {
	case ActionSendEmail:
		var m provider.OutgoingEmail
		if err := json.Unmarshal(a.Payload, &m); err != nil {
			return nil, err
		}
		ch, err := decode[emailChanges](raw)
		if err != nil {
			return nil, err
		}
		m = ch.apply(m)
		if len(m.To) == 0 {
			return nil, fmt.Errorf("the email would have no recipients")
		}
		summary, payload = emailSummary("Email", m), m

	case ActionCreateEvent:
		var ne provider.NewEvent
		if err := json.Unmarshal(a.Payload, &ne); err != nil {
			return nil, err
		}
		ch, err := decode[eventChanges](raw)
		if err != nil {
			return nil, err
		}
		ev := provider.Event{Title: ne.Title, Description: ne.Description, Location: ne.Location, Start: ne.Start, End: ne.End, Attendees: ne.Attendees}
		u, err := t.plan(ev, ch)
		if err != nil {
			return nil, err
		}
		ev = applyUpdate(ev, u)
		ne = provider.NewEvent{Title: ev.Title, Description: ev.Description, Location: ev.Location, Start: ev.Start, End: ev.End, Attendees: ev.Attendees}
		summary, payload = newEventSummary(t, ne), ne

	case ActionUpdateEvent:
		var p eventUpdate
		if err := json.Unmarshal(a.Payload, &p); err != nil {
			return nil, err
		}
		ch, err := decode[eventChanges](raw)
		if err != nil {
			return nil, err
		}
		cur, err := t.cal.GetEvent(ctx, "", p.EventID)
		if err != nil {
			return nil, err
		}
		d, err := t.plan(applyUpdate(cur, p.Update), ch)
		if err != nil {
			return nil, err
		}
		p.Update = overlay(p.Update, d)
		summary, payload = t.updateSummary(cur.Title, cur, p.Update), p
	}

	a, err = t.actions.UpdatePending(ctx, uid, id, summary, payload)
	if err != nil {
		return nil, err
	}
	return map[string]any{"status": "pending_action_updated", "action_id": a.ID, "summary": a.Summary, "payload": a.Payload}, nil
}
