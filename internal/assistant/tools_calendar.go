package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sanjayrj/ai-bot/internal/provider"
)

// eventChanges is a partial edit to an event. attendees replaces the list;
// add_attendees / remove_attendees adjust it.
type eventChanges struct {
	Title           *string   `json:"title"`
	Description     *string   `json:"description"`
	Location        *string   `json:"location"`
	Start           *string   `json:"start"`
	End             *string   `json:"end"`
	Attendees       *[]string `json:"attendees"`
	AddAttendees    []string  `json:"add_attendees"`
	RemoveAttendees []string  `json:"remove_attendees"`
}

var eventChangeProps = prop{
	"title":            str("New title"),
	"description":      str("New description / agenda"),
	"location":         str("New location or video link (empty string clears it)"),
	"start":            str("New start, RFC 3339. If end is omitted the duration is kept."),
	"end":              str("New end, RFC 3339"),
	"attendees":        strs("Replace the whole attendee list"),
	"add_attendees":    strs("Attendee emails to add (they get an invite)"),
	"remove_attendees": strs("Attendee emails to remove"),
}

// eventUpdate is the payload of an update_event action.
type eventUpdate struct {
	EventID string               `json:"event_id"`
	Title   string               `json:"current_title"`
	Update  provider.EventUpdate `json:"update"`
	// Before is the event as it was when the change was queued, so the
	// approval card can show before and after.
	Before *provider.Event `json:"before,omitempty"`
}

// eventRef is the payload of a delete_event action.
type eventRef struct {
	EventID string `json:"event_id"`
	Title   string `json:"title"`
	Start   string `json:"start"`
	// Event is a snapshot of what will be cancelled, for the approval preview.
	Event *provider.Event `json:"event,omitempty"`
}

// plan turns changes into an EventUpdate against the event's current state.
func (t *Toolbox) plan(cur provider.Event, c eventChanges) (provider.EventUpdate, error) {
	var u provider.EventUpdate
	u.Title, u.Description, u.Location = c.Title, c.Description, c.Location

	start, end := cur.Start, cur.End
	if c.Start != nil {
		s, err := t.parseTime(*c.Start)
		if err != nil {
			return u, err
		}
		if c.End == nil {
			end = s.Add(cur.End.Sub(cur.Start)) // keep the duration
		}
		start = s
	}
	if c.End != nil {
		e, err := t.parseTime(*c.End)
		if err != nil {
			return u, err
		}
		end = e
	}
	if !end.After(start) {
		return u, errors.New("end must be after start")
	}
	if !start.Equal(cur.Start) {
		u.Start = &start
	}
	if !end.Equal(cur.End) {
		u.End = &end
	}

	if c.Attendees != nil || len(c.AddAttendees) > 0 || len(c.RemoveAttendees) > 0 {
		list := append([]string(nil), cur.Attendees...)
		if c.Attendees != nil {
			list = append([]string(nil), (*c.Attendees)...)
		}
		list = without(addUnique(list, c.AddAttendees), c.RemoveAttendees)
		u.Attendees = &list
	}
	return u, nil
}

// applyUpdate returns ev with u applied (used to preview pending edits).
func applyUpdate(ev provider.Event, u provider.EventUpdate) provider.Event {
	if u.Title != nil {
		ev.Title = *u.Title
	}
	if u.Description != nil {
		ev.Description = *u.Description
	}
	if u.Location != nil {
		ev.Location = *u.Location
	}
	if u.Start != nil {
		ev.Start = *u.Start
	}
	if u.End != nil {
		ev.End = *u.End
	}
	if u.Attendees != nil {
		ev.Attendees = *u.Attendees
	}
	return ev
}

// overlay merges b's set fields over a.
func overlay(a, b provider.EventUpdate) provider.EventUpdate {
	if b.Title != nil {
		a.Title = b.Title
	}
	if b.Description != nil {
		a.Description = b.Description
	}
	if b.Location != nil {
		a.Location = b.Location
	}
	if b.Start != nil {
		a.Start = b.Start
	}
	if b.End != nil {
		a.End = b.End
	}
	if b.Attendees != nil {
		a.Attendees = b.Attendees
	}
	return a
}

func (t *Toolbox) updateSummary(title string, before provider.Event, u provider.EventUpdate) string {
	var parts []string
	if u.Title != nil {
		parts = append(parts, fmt.Sprintf("title → %q", *u.Title))
	}
	if u.Start != nil || u.End != nil {
		after := applyUpdate(before, u)
		parts = append(parts, fmt.Sprintf("time → %s–%s", t.fmtTime(after.Start), after.End.In(t.loc).Format("15:04")))
	}
	if u.Location != nil {
		parts = append(parts, fmt.Sprintf("location → %q", *u.Location))
	}
	if u.Description != nil {
		parts = append(parts, "description updated")
	}
	if u.Attendees != nil {
		added := without(*u.Attendees, before.Attendees)
		removed := without(before.Attendees, *u.Attendees)
		for _, a := range added {
			parts = append(parts, "+"+a)
		}
		for _, r := range removed {
			parts = append(parts, "−"+r)
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "no changes")
	}
	return fmt.Sprintf("Update %q: %s", title, strings.Join(parts, ", "))
}

func newEventSummary(t *Toolbox, ev provider.NewEvent) string {
	s := fmt.Sprintf("Event %q %s–%s", ev.Title, t.fmtTime(ev.Start), ev.End.In(t.loc).Format("15:04"))
	if ev.Location != "" {
		s += " @ " + ev.Location
	}
	if len(ev.Attendees) > 0 {
		s += " with " + strings.Join(ev.Attendees, ", ")
	}
	return s
}

func (t *Toolbox) registerCalendar() {
	t.add("list_events",
		"List calendar events in a time range (RFC 3339 timestamps). Returns event IDs for get/update/delete_event. "+
			"Pass calendar (a colleague's email) to read their calendar; that only works if they share event details with the user — otherwise use check_availability.",
		prop{"start": str("Range start, RFC 3339"), "end": str("Range end, RFC 3339"), "calendar": str("Whose calendar: a colleague's email; omit for the user's own")},
		[]string{"start", "end"},
		func(ctx context.Context, raw json.RawMessage) (any, error) {
			in, err := decode[struct{ Start, End, Calendar string }](raw)
			if err != nil {
				return nil, err
			}
			from, err := t.parseTime(in.Start)
			if err != nil {
				return nil, err
			}
			to, err := t.parseTime(in.End)
			if err != nil {
				return nil, err
			}
			return t.cal.ListEvents(ctx, in.Calendar, from, to)
		})

	t.add("get_event", "Get one calendar event by ID, including attendees, calendar link and Meet link.",
		prop{"event_id": str("Event ID"), "calendar": str("Colleague's email if the event is on their calendar")}, []string{"event_id"},
		func(ctx context.Context, raw json.RawMessage) (any, error) {
			in, err := decode[struct {
				EventID  string `json:"event_id"`
				Calendar string `json:"calendar"`
			}](raw)
			if err != nil {
				return nil, err
			}
			return t.cal.GetEvent(ctx, in.Calendar, in.EventID)
		})

	t.add("find_free_slots",
		"Find open slots on a given day within working hours, based on the user's calendar.",
		prop{
			"date":             str("Day to search, YYYY-MM-DD"),
			"duration_minutes": num("Required slot length in minutes"),
			"day_start":        str("Working day start HH:MM, default 09:00"),
			"day_end":          str("Working day end HH:MM, default 18:00"),
		},
		[]string{"date", "duration_minutes"},
		func(ctx context.Context, raw json.RawMessage) (any, error) {
			in, err := decode[struct {
				Date     string `json:"date"`
				Duration int    `json:"duration_minutes"`
				DayStart string `json:"day_start"`
				DayEnd   string `json:"day_end"`
			}](raw)
			if err != nil {
				return nil, err
			}
			return t.freeSlots(ctx, in.Date, in.Duration, in.DayStart, in.DayEnd)
		})

	t.add("create_event",
		t.writeDesc("Create a calendar event (invites are sent to attendees)."),
		prop{
			"title": str("Event title"), "description": str("Agenda / notes"), "location": str("Location or video link"),
			"start": str("Start, RFC 3339"), "end": str("End, RFC 3339"), "attendees": strs("Attendee emails"),
		},
		[]string{"title", "start", "end"},
		func(ctx context.Context, raw json.RawMessage) (any, error) {
			in, err := decode[struct {
				Title, Description, Location, Start, End string
				Attendees                                []string
			}](raw)
			if err != nil {
				return nil, err
			}
			start, err := t.parseTime(in.Start)
			if err != nil {
				return nil, err
			}
			end, err := t.parseTime(in.End)
			if err != nil {
				return nil, err
			}
			if !end.After(start) {
				return nil, errors.New("end must be after start")
			}
			ev := provider.NewEvent{Title: in.Title, Description: in.Description, Location: in.Location, Start: start, End: end, Attendees: in.Attendees}
			return t.queue(ctx, ActionCreateEvent, newEventSummary(t, ev), ev)
		})

	t.add("update_event",
		t.writeDesc("Change an existing event: title, time, location, description or attendees. Only the fields you pass change; Google emails the updated invite to attendees (new attendees get an invitation with the time and link)."),
		merge(prop{"event_id": str("Event ID from list_events")}, eventChangeProps), []string{"event_id"},
		func(ctx context.Context, raw json.RawMessage) (any, error) {
			in, err := decode[struct {
				EventID string `json:"event_id"`
				eventChanges
			}](raw)
			if err != nil {
				return nil, err
			}
			cur, err := t.cal.GetEvent(ctx, "", in.EventID)
			if err != nil {
				return nil, err
			}
			u, err := t.plan(cur, in.eventChanges)
			if err != nil {
				return nil, err
			}
			p := eventUpdate{EventID: cur.ID, Title: cur.Title, Update: u, Before: &cur}
			return t.queue(ctx, ActionUpdateEvent, t.updateSummary(cur.Title, cur, u), p)
		})

	t.add("delete_event",
		t.writeDesc("Cancel and delete a calendar event. Attendees are notified."),
		prop{"event_id": str("Event ID from list_events")}, []string{"event_id"},
		func(ctx context.Context, raw json.RawMessage) (any, error) {
			in, err := decode[eventRef](raw)
			if err != nil {
				return nil, err
			}
			cur, err := t.cal.GetEvent(ctx, "", in.EventID)
			if err != nil {
				return nil, err
			}
			p := eventRef{EventID: cur.ID, Title: cur.Title, Start: cur.Start.Format(time.RFC3339), Event: &cur}
			return t.queue(ctx, ActionDeleteEvent, fmt.Sprintf("Cancel %q %s", cur.Title, t.fmtTime(cur.Start)), p)
		})
}
