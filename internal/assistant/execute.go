package assistant

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sanjayrj/ai-bot/internal/provider"
)

// execute carries out an approved action against the real integration.
func (t *Toolbox) execute(ctx context.Context, a Action) (string, error) {
	switch a.Kind {
	case ActionSendEmail:
		var m provider.OutgoingEmail
		if err := json.Unmarshal(a.Payload, &m); err != nil {
			return "", err
		}
		id, err := t.mail.Send(ctx, m)
		if err != nil {
			return "", err
		}
		return "Email sent (id " + id + ")", nil

	case ActionSendDraft, ActionDeleteDraft:
		var r draftRef
		if err := json.Unmarshal(a.Payload, &r); err != nil {
			return "", err
		}
		if a.Kind == ActionDeleteDraft {
			if err := t.mail.DeleteDraft(ctx, r.DraftID); err != nil {
				return "", err
			}
			return "Draft deleted", nil
		}
		id, err := t.mail.SendDraft(ctx, r.DraftID)
		if err != nil {
			return "", err
		}
		return "Draft sent (id " + id + ")", nil

	case ActionCreateEvent:
		var ev provider.NewEvent
		if err := json.Unmarshal(a.Payload, &ev); err != nil {
			return "", err
		}
		created, err := t.cal.CreateEvent(ctx, ev)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Event created: %s", firstNonEmpty(created.Link, created.ID)), nil

	case ActionUpdateEvent:
		var p eventUpdate
		if err := json.Unmarshal(a.Payload, &p); err != nil {
			return "", err
		}
		ev, err := t.cal.UpdateEvent(ctx, p.EventID, p.Update)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Event updated: %s", firstNonEmpty(ev.Link, ev.ID)), nil

	case ActionDeleteEvent:
		var r eventRef
		if err := json.Unmarshal(a.Payload, &r); err != nil {
			return "", err
		}
		if err := t.cal.DeleteEvent(ctx, r.EventID); err != nil {
			return "", err
		}
		return fmt.Sprintf("Event %q cancelled", r.Title), nil
	}
	return "", fmt.Errorf("unknown action kind %q", a.Kind)
}
