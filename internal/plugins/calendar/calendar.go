// Package calendar is the Google Calendar plugin: read events (yours or
// shared colleagues'), free/busy across people, and create, change or cancel
// events as approvable actions.
package calendar

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/sanjayrj/ai-bot/internal/plugin"
	"github.com/sanjayrj/ai-bot/internal/provider"
)

// Action kinds this plugin executes.
const (
	KindCreateEvent = "create_event"
	KindUpdateEvent = "update_event"
	KindDeleteEvent = "delete_event"
)

// Plugin implements plugin.Plugin, Executor, Editor, Deduper and Guide.
type Plugin struct {
	cal      provider.Calendar
	clock    plugin.Clock
	settings plugin.Settings // protected-time rules; may be nil
}

func New(cal provider.Calendar, loc *time.Location, settings plugin.Settings) *Plugin {
	return &Plugin{cal: cal, clock: plugin.Clock{Loc: loc}, settings: settings}
}

var (
	_ plugin.Executor = (*Plugin)(nil)
	_ plugin.Editor   = (*Plugin)(nil)
	_ plugin.Deduper  = (*Plugin)(nil)
	_ plugin.Guide    = (*Plugin)(nil)
)

func (p *Plugin) Name() string { return "calendar" }

func (p *Plugin) Guidance() string {
	return `- Calendar: check the calendar and use find_free_slots before proposing times; use the user's time zone. Find event IDs with list_events first; never guess IDs.
- To change a meeting use update_event (title, time, location, description, attendees); to add people use add_attendees so Google sends them the invite with the time and link. delete_event cancels.
- Colleagues: use find_common_free_slots or check_availability (busy times only), then create_event with them as attendees. To see a colleague's meetings call list_events with calendar set to their email; if it isn't shared, say so. Never present busy blocks as meeting details.
- Protected time: the user's working hours and protected blocks (set_time_rules) are skipped by slot search; mention skipped blocks when relevant. If a booking's summary carries a ⚠ warning (a clash or protected time), tell the user before they approve; only book over protected time when they insist.`
}

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

var changeProps = map[string]any{
	"title":            plugin.Str("New title"),
	"description":      plugin.Str("New description / agenda"),
	"location":         plugin.Str("New location or video link (empty string clears it)"),
	"start":            plugin.Str("New start, RFC 3339. If end is omitted the duration is kept."),
	"end":              plugin.Str("New end, RFC 3339"),
	"attendees":        plugin.Strs("Replace the whole attendee list"),
	"add_attendees":    plugin.Strs("Attendee emails to add (they get an invite)"),
	"remove_attendees": plugin.Strs("Attendee emails to remove"),
}

// eventUpdate is the payload of an update_event action. Before snapshots the
// event when queued so the approval card can show before and after.
type eventUpdate struct {
	EventID  string               `json:"event_id"`
	Title    string               `json:"current_title"`
	Update   provider.EventUpdate `json:"update"`
	Before   *provider.Event      `json:"before,omitempty"`
	Warnings []string             `json:"warnings,omitempty"`
}

// newEvent is the payload of a create_event action; Warnings are the clashes
// found when it was proposed, shown on the approval card.
type newEvent struct {
	provider.NewEvent
	Warnings []string `json:"warnings,omitempty"`
}

// eventRef is the payload of a delete_event action.
type eventRef struct {
	EventID string          `json:"event_id"`
	Title   string          `json:"title"`
	Start   string          `json:"start"`
	Event   *provider.Event `json:"event,omitempty"`
}

func (p *Plugin) Tools() []plugin.Tool {
	people := plugin.Strs(`Email addresses of colleagues; use "me" for the user`)
	return append(p.ruleTools(), []plugin.Tool{
		{
			Name: "list_events",
			Description: "List calendar events in a time range (RFC 3339 timestamps). Returns event IDs for get/update/delete_event. " +
				"Pass calendar (a colleague's email) to read their calendar; that only works if they share event details with the user — otherwise use check_availability.",
			Properties: map[string]any{"start": plugin.Str("Range start, RFC 3339"), "end": plugin.Str("Range end, RFC 3339"), "calendar": plugin.Str("Whose calendar: a colleague's email; omit for the user's own")},
			Required:   []string{"start", "end"},
			Run: func(ctx context.Context, raw json.RawMessage) (any, error) {
				in, err := plugin.Decode[struct{ Start, End, Calendar string }](raw)
				if err != nil {
					return nil, err
				}
				from, err := p.clock.Parse(in.Start)
				if err != nil {
					return nil, err
				}
				to, err := p.clock.Parse(in.End)
				if err != nil {
					return nil, err
				}
				return p.cal.ListEvents(ctx, in.Calendar, from, to)
			},
		},
		{
			Name:        "get_event",
			Description: "Get one calendar event by ID, including attendees, calendar link and Meet link.",
			Properties:  map[string]any{"event_id": plugin.Str("Event ID"), "calendar": plugin.Str("Colleague's email if the event is on their calendar")},
			Required:    []string{"event_id"},
			Run: func(ctx context.Context, raw json.RawMessage) (any, error) {
				in, err := plugin.Decode[struct {
					EventID  string `json:"event_id"`
					Calendar string `json:"calendar"`
				}](raw)
				if err != nil {
					return nil, err
				}
				return p.cal.GetEvent(ctx, in.Calendar, in.EventID)
			},
		},
		{
			Name:        "find_free_slots",
			Description: "Find open slots on a given day within the user's working hours, skipping their protected time, based on their calendar.",
			Properties: map[string]any{
				"date":              plugin.Str("Day to search, YYYY-MM-DD"),
				"duration_minutes":  plugin.Num("Required slot length in minutes"),
				"day_start":         plugin.Str("Search from HH:MM; default the user's work start (or 09:00)"),
				"day_end":           plugin.Str("Search until HH:MM; default the user's work end (or 18:00)"),
				"include_protected": map[string]any{"type": "boolean", "description": "Also offer the user's protected time (only if they ask)"},
			},
			Required: []string{"date", "duration_minutes"},
			Run: func(ctx context.Context, raw json.RawMessage) (any, error) {
				in, err := plugin.Decode[slotQuery](raw)
				if err != nil {
					return nil, err
				}
				return p.searchSlots(ctx, in, nil)
			},
		},
		{
			Name:        "check_availability",
			Description: "Check whether people are free or busy in a time range, using Google free/busy. Works for colleagues in the same Workspace without them sharing their calendar (shows busy times, not meeting details).",
			Properties:  map[string]any{"people": people, "start": plugin.Str("Range start, RFC 3339"), "end": plugin.Str("Range end, RFC 3339")},
			Required:    []string{"people", "start", "end"},
			Run:         p.checkAvailability,
		},
		{
			Name:        "find_common_free_slots",
			Description: "Find times on a day when the user and all the given people are free (Google free/busy), e.g. to schedule a meeting. People whose calendars can't be read are listed in unknown and NOT counted as free.",
			Properties: map[string]any{
				"people":            people,
				"date":              plugin.Str("Day to search, YYYY-MM-DD"),
				"duration_minutes":  plugin.Num("Required slot length in minutes"),
				"day_start":         plugin.Str("Search from HH:MM; default the user's work start (or 09:00)"),
				"day_end":           plugin.Str("Search until HH:MM; default the user's work end (or 18:00)"),
				"include_protected": map[string]any{"type": "boolean", "description": "Also offer the user's protected time (only if they ask)"},
			},
			Required: []string{"people", "date", "duration_minutes"},
			Run:      p.commonSlots,
		},
		{
			Name:        "create_event",
			Description: "Create a calendar event (invites are sent to attendees).",
			Properties: map[string]any{
				"title": plugin.Str("Event title"), "description": plugin.Str("Agenda / notes"), "location": plugin.Str("Location or video link"),
				"start": plugin.Str("Start, RFC 3339"), "end": plugin.Str("End, RFC 3339"), "attendees": plugin.Strs("Attendee emails"),
			},
			Required: []string{"title", "start", "end"},
			Write:    true,
			Propose: func(ctx context.Context, raw json.RawMessage) (plugin.Proposal, error) {
				in, err := plugin.Decode[struct {
					Title, Description, Location, Start, End string
					Attendees                                []string
				}](raw)
				if err != nil {
					return plugin.Proposal{}, err
				}
				start, err := p.clock.Parse(in.Start)
				if err != nil {
					return plugin.Proposal{}, err
				}
				end, err := p.clock.Parse(in.End)
				if err != nil {
					return plugin.Proposal{}, err
				}
				if !end.After(start) {
					return plugin.Proposal{}, errors.New("end must be after start")
				}
				ev := provider.NewEvent{Title: in.Title, Description: in.Description, Location: in.Location, Start: start, End: end, Attendees: in.Attendees}
				return p.createProposal(ctx, ev), nil
			},
		},
		{
			Name:        "update_event",
			Description: "Change an existing event: title, time, location, description or attendees. Only the fields you pass change; Google emails the updated invite to attendees (new attendees get an invitation with the time and link).",
			Properties:  plugin.Props(map[string]any{"event_id": plugin.Str("Event ID from list_events")}, changeProps),
			Required:    []string{"event_id"},
			Write:       true,
			Propose: func(ctx context.Context, raw json.RawMessage) (plugin.Proposal, error) {
				in, err := plugin.Decode[struct {
					EventID string `json:"event_id"`
					eventChanges
				}](raw)
				if err != nil {
					return plugin.Proposal{}, err
				}
				cur, err := p.cal.GetEvent(ctx, "", in.EventID)
				if err != nil {
					return plugin.Proposal{}, err
				}
				u, err := p.plan(cur, in.eventChanges)
				if err != nil {
					return plugin.Proposal{}, err
				}
				return p.updateProposal(ctx, eventUpdate{EventID: cur.ID, Title: cur.Title, Update: u, Before: &cur}, cur), nil
			},
		},
		{
			Name:        "delete_event",
			Description: "Cancel and delete a calendar event. Attendees are notified.",
			Properties:  map[string]any{"event_id": plugin.Str("Event ID from list_events")},
			Required:    []string{"event_id"},
			Write:       true,
			Propose: func(ctx context.Context, raw json.RawMessage) (plugin.Proposal, error) {
				in, err := plugin.Decode[eventRef](raw)
				if err != nil {
					return plugin.Proposal{}, err
				}
				cur, err := p.cal.GetEvent(ctx, "", in.EventID)
				if err != nil {
					return plugin.Proposal{}, err
				}
				pl := eventRef{EventID: cur.ID, Title: cur.Title, Start: cur.Start.Format(time.RFC3339), Event: &cur}
				return plugin.Proposal{Kind: KindDeleteEvent, Summary: fmt.Sprintf("Cancel %q %s", cur.Title, p.clock.Short(cur.Start)), Payload: pl}, nil
			},
		},
	}...)
}

// ---- planning edits ----

// plan turns changes into an EventUpdate against the event's current state.
func (p *Plugin) plan(cur provider.Event, c eventChanges) (provider.EventUpdate, error) {
	var u provider.EventUpdate
	u.Title, u.Description, u.Location = c.Title, c.Description, c.Location

	start, end := cur.Start, cur.End
	if c.Start != nil {
		s, err := p.clock.Parse(*c.Start)
		if err != nil {
			return u, err
		}
		if c.End == nil {
			end = s.Add(cur.End.Sub(cur.Start)) // keep the duration
		}
		start = s
	}
	if c.End != nil {
		e, err := p.clock.Parse(*c.End)
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
		list = plugin.Without(plugin.AddUnique(list, c.AddAttendees), c.RemoveAttendees)
		u.Attendees = &list
	}
	return u, nil
}

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

func (p *Plugin) updateSummary(title string, before provider.Event, u provider.EventUpdate) string {
	var parts []string
	if u.Title != nil {
		parts = append(parts, fmt.Sprintf("title → %q", *u.Title))
	}
	if u.Start != nil || u.End != nil {
		after := applyUpdate(before, u)
		parts = append(parts, fmt.Sprintf("time → %s–%s", p.clock.Short(after.Start), after.End.In(p.clock.Loc).Format("15:04")))
	}
	if u.Location != nil {
		parts = append(parts, fmt.Sprintf("location → %q", *u.Location))
	}
	if u.Description != nil {
		parts = append(parts, "description updated")
	}
	if u.Attendees != nil {
		for _, a := range plugin.Without(*u.Attendees, before.Attendees) {
			parts = append(parts, "+"+a)
		}
		for _, r := range plugin.Without(before.Attendees, *u.Attendees) {
			parts = append(parts, "−"+r)
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "no changes")
	}
	return fmt.Sprintf("Update %q: %s", title, strings.Join(parts, ", "))
}

func (p *Plugin) newSummary(ev provider.NewEvent) string {
	s := fmt.Sprintf("Event %q %s–%s", ev.Title, p.clock.Short(ev.Start), ev.End.In(p.clock.Loc).Format("15:04"))
	if ev.Location != "" {
		s += " @ " + ev.Location
	}
	if len(ev.Attendees) > 0 {
		s += " with " + strings.Join(ev.Attendees, ", ")
	}
	return s
}

func (p *Plugin) createProposal(ctx context.Context, ev provider.NewEvent) plugin.Proposal {
	w := p.warnings(ctx, ev.Start, ev.End, "")
	return plugin.Proposal{Kind: KindCreateEvent, Summary: withWarnings(p.newSummary(ev), w), Payload: newEvent{ev, w}}
}

// updateProposal checks the new time (if it moves) against rules and events.
func (p *Plugin) updateProposal(ctx context.Context, pl eventUpdate, cur provider.Event) plugin.Proposal {
	pl.Warnings = nil
	if pl.Update.Start != nil || pl.Update.End != nil {
		after := applyUpdate(cur, pl.Update)
		pl.Warnings = p.warnings(ctx, after.Start, after.End, cur.ID)
	}
	return plugin.Proposal{Kind: KindUpdateEvent, Summary: withWarnings(p.updateSummary(cur.Title, cur, pl.Update), pl.Warnings), Payload: pl}
}

// ---- Executor ----

func (p *Plugin) Kinds() []string { return []string{KindCreateEvent, KindUpdateEvent, KindDeleteEvent} }

func (p *Plugin) Execute(ctx context.Context, kind string, payload json.RawMessage) (string, error) {
	switch kind {
	case KindCreateEvent:
		var ev provider.NewEvent
		if err := json.Unmarshal(payload, &ev); err != nil {
			return "", err
		}
		created, err := p.cal.CreateEvent(ctx, ev)
		if err != nil {
			return "", err
		}
		return "Event created: " + plugin.FirstNonEmpty(created.Link, created.ID), nil
	case KindUpdateEvent:
		var pl eventUpdate
		if err := json.Unmarshal(payload, &pl); err != nil {
			return "", err
		}
		ev, err := p.cal.UpdateEvent(ctx, pl.EventID, pl.Update)
		if err != nil {
			return "", err
		}
		return "Event updated: " + plugin.FirstNonEmpty(ev.Link, ev.ID), nil
	case KindDeleteEvent:
		var r eventRef
		if err := json.Unmarshal(payload, &r); err != nil {
			return "", err
		}
		if err := p.cal.DeleteEvent(ctx, r.EventID); err != nil {
			return "", err
		}
		return fmt.Sprintf("Event %q cancelled", r.Title), nil
	}
	return "", fmt.Errorf("calendar: unknown action kind %q", kind)
}

// ---- Editor ----

func (p *Plugin) EditFields(kind string) map[string]any {
	if kind == KindCreateEvent || kind == KindUpdateEvent {
		return changeProps
	}
	return nil
}

func (p *Plugin) Edit(ctx context.Context, kind string, payload, raw json.RawMessage) (plugin.Proposal, error) {
	ch, err := plugin.Decode[eventChanges](raw)
	if err != nil {
		return plugin.Proposal{}, err
	}
	switch kind {
	case KindCreateEvent:
		var ne provider.NewEvent
		if err := json.Unmarshal(payload, &ne); err != nil {
			return plugin.Proposal{}, err
		}
		ev := provider.Event{Title: ne.Title, Description: ne.Description, Location: ne.Location, Start: ne.Start, End: ne.End, Attendees: ne.Attendees}
		u, err := p.plan(ev, ch)
		if err != nil {
			return plugin.Proposal{}, err
		}
		ev = applyUpdate(ev, u)
		ne = provider.NewEvent{Title: ev.Title, Description: ev.Description, Location: ev.Location, Start: ev.Start, End: ev.End, Attendees: ev.Attendees}
		return p.createProposal(ctx, ne), nil
	case KindUpdateEvent:
		var pl eventUpdate
		if err := json.Unmarshal(payload, &pl); err != nil {
			return plugin.Proposal{}, err
		}
		cur, err := p.cal.GetEvent(ctx, "", pl.EventID)
		if err != nil {
			return plugin.Proposal{}, err
		}
		d, err := p.plan(applyUpdate(cur, pl.Update), ch)
		if err != nil {
			return plugin.Proposal{}, err
		}
		pl.Update = overlay(pl.Update, d)
		return p.updateProposal(ctx, pl, cur), nil
	}
	return plugin.Proposal{}, fmt.Errorf("a %s action can't be edited", kind)
}

// ---- Deduper ----

// Duplicate: a new event with the same title at overlapping times, or a
// second pending change/cancellation of the same event.
func (p *Plugin) Duplicate(kind string, pending, proposed json.RawMessage) bool {
	switch kind {
	case KindCreateEvent:
		var a, b provider.NewEvent
		if json.Unmarshal(pending, &a) != nil || json.Unmarshal(proposed, &b) != nil {
			return false
		}
		return plugin.Norm(a.Title) == plugin.Norm(b.Title) && a.Start.Before(b.End) && b.Start.Before(a.End)
	case KindUpdateEvent, KindDeleteEvent:
		var a, b struct {
			EventID string `json:"event_id"`
		}
		_ = json.Unmarshal(pending, &a)
		_ = json.Unmarshal(proposed, &b)
		return a.EventID != "" && a.EventID == b.EventID
	}
	return false
}

// ---- free/busy ----

type slotQuery struct {
	People           []string `json:"people"`
	Date             string   `json:"date"`
	Duration         int      `json:"duration_minutes"`
	DayStart         string   `json:"day_start"`
	DayEnd           string   `json:"day_end"`
	IncludeProtected bool     `json:"include_protected"`
}

// searchSlots finds free time on q.Date honouring the user's rules. extra
// is other people's busy time (for common slots).
func (p *Plugin) searchSlots(ctx context.Context, q slotQuery, extra []provider.Busy) (map[string]any, error) {
	if q.Duration <= 0 {
		q.Duration = 30
	}
	r := p.rules(ctx)
	from, to, err := p.dayWindow(q.Date, plugin.FirstNonEmpty(q.DayStart, r.WorkStart), plugin.FirstNonEmpty(q.DayEnd, r.WorkEnd))
	if err != nil {
		return nil, err
	}
	busy, err := p.ownBusy(ctx, from, to)
	if err != nil {
		return nil, err
	}
	busy = append(busy, extra...)
	out := map[string]any{}
	if !q.IncludeProtected {
		var skipped []string
		for _, b := range r.protectedOn(from) {
			if b.Start.Before(to) && from.Before(b.End) {
				busy = append(busy, b.Busy)
				skipped = append(skipped, fmt.Sprintf("%s %s–%s", b.Label, b.Start.Format("15:04"), b.End.Format("15:04")))
			}
		}
		if len(skipped) > 0 {
			out["skipped_protected_time"] = skipped
		}
	}
	if !r.works(from) {
		out["note"] = from.Format("Monday") + " isn't one of the user's working days; mention it before booking."
	}
	out["slots"] = gaps(from, to, busy, time.Duration(q.Duration)*time.Minute)
	return out, nil
}

func (p *Plugin) ownBusy(ctx context.Context, from, to time.Time) ([]provider.Busy, error) {
	events, err := p.cal.ListEvents(ctx, "", from, to)
	if err != nil {
		return nil, err
	}
	var busy []provider.Busy
	for _, e := range events {
		if !e.AllDay {
			busy = append(busy, provider.Busy{Start: e.Start, End: e.End})
		}
	}
	return busy, nil
}

// Slot is a free time range.
type Slot struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// busyView is one person's availability, rendered for the model.
type busyView struct {
	Person string       `json:"person"`
	Free   *bool        `json:"free,omitempty"` // nil when unknown
	Busy   []busyWindow `json:"busy,omitempty"`
	Error  string       `json:"error,omitempty"`
}

type busyWindow struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

func (p *Plugin) dayWindow(date, dayStart, dayEnd string) (time.Time, time.Time, error) {
	if dayStart == "" {
		dayStart = "09:00"
	}
	if dayEnd == "" {
		dayEnd = "18:00"
	}
	from, err := time.ParseInLocation("2006-01-02 15:04", date+" "+dayStart, p.clock.Loc)
	if err != nil {
		return from, from, fmt.Errorf("bad date/day_start: %w", err)
	}
	to, err := time.ParseInLocation("2006-01-02 15:04", date+" "+dayEnd, p.clock.Loc)
	if err != nil {
		return from, to, fmt.Errorf("bad date/day_end: %w", err)
	}
	return from, to, nil
}

// FreeSlots finds open stretches on the user's own calendar.
func (p *Plugin) FreeSlots(ctx context.Context, date string, minutes int, dayStart, dayEnd string) ([]Slot, error) {
	if minutes <= 0 {
		minutes = 30
	}
	from, to, err := p.dayWindow(date, dayStart, dayEnd)
	if err != nil {
		return nil, err
	}
	busy, err := p.ownBusy(ctx, from, to)
	if err != nil {
		return nil, err
	}
	return gaps(from, to, busy, time.Duration(minutes)*time.Minute), nil
}

func (p *Plugin) checkAvailability(ctx context.Context, raw json.RawMessage) (any, error) {
	in, err := plugin.Decode[struct {
		People     []string
		Start, End string
	}](raw)
	if err != nil {
		return nil, err
	}
	from, err := p.clock.Parse(in.Start)
	if err != nil {
		return nil, err
	}
	to, err := p.clock.Parse(in.End)
	if err != nil {
		return nil, err
	}
	if !to.After(from) {
		return nil, errors.New("end must be after start")
	}
	ids := cleanPeople(in.People)
	res, err := p.cal.FreeBusy(ctx, ids, from, to)
	if err != nil {
		return nil, err
	}
	out := make([]busyView, 0, len(ids))
	for _, id := range ids {
		a := res[id]
		v := busyView{Person: id, Error: a.Error}
		if a.Error == "" {
			free := len(a.Busy) == 0
			v.Free = &free
			for _, b := range a.Busy {
				v.Busy = append(v.Busy, busyWindow{p.clock.Short(b.Start), p.clock.Short(b.End)})
			}
		}
		out = append(out, v)
	}
	return out, nil
}

func (p *Plugin) commonSlots(ctx context.Context, raw json.RawMessage) (any, error) {
	in, err := plugin.Decode[slotQuery](raw)
	if err != nil {
		return nil, err
	}
	r := p.rules(ctx)
	from, to, err := p.dayWindow(in.Date, plugin.FirstNonEmpty(in.DayStart, r.WorkStart), plugin.FirstNonEmpty(in.DayEnd, r.WorkEnd))
	if err != nil {
		return nil, err
	}
	ids := cleanPeople(append([]string{"me"}, in.People...))
	res, err := p.cal.FreeBusy(ctx, ids, from, to)
	if err != nil {
		return nil, err
	}
	var busy []provider.Busy
	unknown := map[string]string{}
	for _, id := range ids {
		if res[id].Error != "" {
			unknown[id] = res[id].Error
			continue
		}
		busy = append(busy, res[id].Busy...)
	}
	out, err := p.searchSlots(ctx, in, busy)
	if err != nil {
		return nil, err
	}
	out["checked"] = ids
	if len(unknown) > 0 {
		out["unknown"] = unknown
		out["note"] = "Some calendars could not be read; these slots only account for the others."
	}
	return out, nil
}

// gaps returns free stretches of at least need between from and to,
// skipping time already past.
func gaps(from, to time.Time, busy []provider.Busy, need time.Duration) []Slot {
	sort.Slice(busy, func(i, j int) bool { return busy[i].Start.Before(busy[j].Start) })
	out := []Slot{}
	cursor := from
	if now := time.Now(); now.After(cursor) && now.Before(to) {
		cursor = now.Truncate(15 * time.Minute).Add(15 * time.Minute)
	}
	for _, b := range busy {
		if b.Start.Sub(cursor) >= need {
			out = append(out, Slot{cursor, b.Start})
		}
		if b.End.After(cursor) {
			cursor = b.End
		}
	}
	if to.Sub(cursor) >= need {
		out = append(out, Slot{cursor, to})
	}
	return out
}

func cleanPeople(in []string) []string {
	var out []string
	for _, p := range in {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" && !plugin.ContainsFold(out, p) {
			out = append(out, p)
		}
	}
	return out
}
