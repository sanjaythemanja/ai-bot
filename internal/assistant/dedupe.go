package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// duplicateOf returns a pending action that the new one would duplicate.
// Smaller models sometimes "fix" a queued email or event by queuing a second
// copy instead of editing the first; this stops that so stale copies can't
// be approved by accident.
func (t *Toolbox) duplicateOf(ctx context.Context, uid string, kind ActionKind, payload any) (Action, bool) {
	pending, err := t.actions.List(ctx, uid, 100)
	if err != nil {
		return Action{}, false
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return Action{}, false
	}
	var next map[string]any
	_ = json.Unmarshal(b, &next)

	for _, a := range pending {
		if a.Status != StatusPending || a.Kind != kind {
			continue
		}
		var prev map[string]any
		if json.Unmarshal(a.Payload, &prev) != nil {
			continue
		}
		if sameAction(kind, prev, next) {
			return a, true
		}
	}
	return Action{}, false
}

func sameAction(kind ActionKind, a, b map[string]any) bool {
	switch kind {
	case ActionSendEmail:
		// Same subject and body (e.g. only the recipient was corrected), or
		// same recipients and subject (only the wording changed).
		sameSubject := norm(a["subject"]) == norm(b["subject"])
		return (sameSubject && norm(a["body"]) == norm(b["body"])) ||
			(sameSubject && addrSet(a["to"]) == addrSet(b["to"]))
	case ActionCreateEvent:
		// Same title at overlapping times.
		return norm(a["title"]) == norm(b["title"]) && overlaps(a, b)
	case ActionUpdateEvent, ActionDeleteEvent:
		return str2(a["event_id"]) != "" && str2(a["event_id"]) == str2(b["event_id"])
	case ActionSendDraft, ActionDeleteDraft:
		return str2(a["draft_id"]) != "" && str2(a["draft_id"]) == str2(b["draft_id"])
	}
	return false
}

func duplicateError(a Action) error {
	return fmt.Errorf("a matching action is already waiting for approval (action_id %s: %s). "+
		"To change it, call edit_pending_action with action_id %s. "+
		"If it should be replaced entirely, call cancel_pending_action on it first", a.ID, a.Summary, a.ID)
}

func str2(v any) string {
	s, _ := v.(string)
	return s
}

func norm(v any) string {
	s := strings.ToLower(str2(v))
	s = strings.ReplaceAll(s, `\n`, " ")
	return strings.Join(strings.Fields(s), " ")
}

func addrSet(v any) string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, x := range list {
		out = append(out, strings.ToLower(strings.TrimSpace(str2(x))))
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

func overlaps(a, b map[string]any) bool {
	as, err1 := time.Parse(time.RFC3339, str2(a["start"]))
	ae, err2 := time.Parse(time.RFC3339, str2(a["end"]))
	bs, err3 := time.Parse(time.RFC3339, str2(b["start"]))
	be, err4 := time.Parse(time.RFC3339, str2(b["end"]))
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
		return false
	}
	return as.Before(be) && bs.Before(ae)
}
