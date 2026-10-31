package google

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"

	"github.com/sanjayrj/ai-bot/internal/provider"
)

// Calendar implements provider.Calendar on the user's primary calendar.
type Calendar struct{ auth *Auth }

// NewCalendar returns a Calendar provider backed by auth.
func NewCalendar(auth *Auth) *Calendar { return &Calendar{auth: auth} }

var _ provider.Calendar = (*Calendar)(nil)

func (c *Calendar) Connected() bool { return c.auth.Connected() }

func (c *Calendar) svc(ctx context.Context) (*calendar.Service, error) {
	hc, err := c.auth.HTTPClient(ctx)
	if err != nil {
		return nil, err
	}
	return calendar.NewService(ctx, option.WithHTTPClient(hc))
}

func calID(id string) string {
	if id == "" || id == "me" {
		return "primary"
	}
	return id
}

func (c *Calendar) ListEvents(ctx context.Context, calendarID string, from, to time.Time) ([]provider.Event, error) {
	s, err := c.svc(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.Events.List(calID(calendarID)).
		TimeMin(from.Format(time.RFC3339)).TimeMax(to.Format(time.RFC3339)).
		SingleEvents(true).OrderBy("startTime").MaxResults(100).Context(ctx).Do()
	if err != nil {
		if calendarID != "" && calendarID != "me" {
			return nil, fmt.Errorf("calendar list %s: %w (for a colleague, they must share their calendar with you)", calendarID, err)
		}
		return nil, fmt.Errorf("calendar list: %w", err)
	}
	out := make([]provider.Event, 0, len(res.Items))
	for _, it := range res.Items {
		if it.Status == "cancelled" {
			continue
		}
		out = append(out, toEvent(it))
	}
	return out, nil
}

func (c *Calendar) CreateEvent(ctx context.Context, ev provider.NewEvent) (provider.Event, error) {
	s, err := c.svc(ctx)
	if err != nil {
		return provider.Event{}, err
	}
	in := &calendar.Event{
		Summary:     ev.Title,
		Description: ev.Description,
		Location:    ev.Location,
		Start:       &calendar.EventDateTime{DateTime: ev.Start.Format(time.RFC3339)},
		End:         &calendar.EventDateTime{DateTime: ev.End.Format(time.RFC3339)},
	}
	for _, a := range ev.Attendees {
		in.Attendees = append(in.Attendees, &calendar.EventAttendee{Email: a})
	}
	created, err := s.Events.Insert("primary", in).SendUpdates("all").Context(ctx).Do()
	if err != nil {
		return provider.Event{}, fmt.Errorf("calendar insert: %w", err)
	}
	return toEvent(created), nil
}

func (c *Calendar) GetEvent(ctx context.Context, calendarID, id string) (provider.Event, error) {
	s, err := c.svc(ctx)
	if err != nil {
		return provider.Event{}, err
	}
	it, err := s.Events.Get(calID(calendarID), id).Context(ctx).Do()
	if err != nil {
		return provider.Event{}, fmt.Errorf("calendar get %s: %w", id, err)
	}
	return toEvent(it), nil
}

// FreeBusy asks Google which of the given calendars are busy when.
func (c *Calendar) FreeBusy(ctx context.Context, calendarIDs []string, from, to time.Time) (map[string]provider.Availability, error) {
	s, err := c.svc(ctx)
	if err != nil {
		return nil, err
	}
	req := &calendar.FreeBusyRequest{TimeMin: from.Format(time.RFC3339), TimeMax: to.Format(time.RFC3339)}
	for _, id := range calendarIDs {
		req.Items = append(req.Items, &calendar.FreeBusyRequestItem{Id: calID(id)})
	}
	res, err := s.Freebusy.Query(req).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("calendar freebusy: %w", err)
	}
	out := map[string]provider.Availability{}
	for _, id := range calendarIDs {
		fb, ok := res.Calendars[calID(id)]
		if !ok {
			out[id] = provider.Availability{Error: "no result returned"}
			continue
		}
		var a provider.Availability
		for _, e := range fb.Errors {
			a.Error = e.Reason
			if a.Error == "notFound" {
				a.Error = "calendar not found or not visible to you (outside your Workspace, or sharing is off)"
			}
		}
		for _, b := range fb.Busy {
			st, _ := time.Parse(time.RFC3339, b.Start)
			en, _ := time.Parse(time.RFC3339, b.End)
			a.Busy = append(a.Busy, provider.Busy{Start: st, End: en})
		}
		out[id] = a
	}
	return out, nil
}

// UpdateEvent patches only the fields that are set and notifies attendees.
func (c *Calendar) UpdateEvent(ctx context.Context, id string, upd provider.EventUpdate) (provider.Event, error) {
	s, err := c.svc(ctx)
	if err != nil {
		return provider.Event{}, err
	}
	patch := &calendar.Event{}
	if upd.Title != nil {
		patch.Summary = *upd.Title
		patch.ForceSendFields = append(patch.ForceSendFields, "Summary")
	}
	if upd.Description != nil {
		patch.Description = *upd.Description
		patch.ForceSendFields = append(patch.ForceSendFields, "Description")
	}
	if upd.Location != nil {
		patch.Location = *upd.Location
		patch.ForceSendFields = append(patch.ForceSendFields, "Location")
	}
	if upd.Start != nil {
		patch.Start = &calendar.EventDateTime{DateTime: upd.Start.Format(time.RFC3339)}
	}
	if upd.End != nil {
		patch.End = &calendar.EventDateTime{DateTime: upd.End.Format(time.RFC3339)}
	}
	if upd.Attendees != nil {
		patch.Attendees = []*calendar.EventAttendee{}
		for _, a := range *upd.Attendees {
			patch.Attendees = append(patch.Attendees, &calendar.EventAttendee{Email: a})
		}
		patch.ForceSendFields = append(patch.ForceSendFields, "Attendees")
	}
	it, err := s.Events.Patch("primary", id, patch).SendUpdates("all").Context(ctx).Do()
	if err != nil {
		return provider.Event{}, fmt.Errorf("calendar patch %s: %w", id, err)
	}
	return toEvent(it), nil
}

// DeleteEvent cancels the event and notifies attendees.
func (c *Calendar) DeleteEvent(ctx context.Context, id string) error {
	s, err := c.svc(ctx)
	if err != nil {
		return err
	}
	if err := s.Events.Delete("primary", id).SendUpdates("all").Context(ctx).Do(); err != nil {
		return fmt.Errorf("calendar delete %s: %w", id, err)
	}
	return nil
}

func toEvent(it *calendar.Event) provider.Event {
	e := provider.Event{
		ID:          it.Id,
		Title:       it.Summary,
		Description: it.Description,
		Location:    it.Location,
		Link:        it.HtmlLink,
		MeetLink:    it.HangoutLink,
	}
	e.Start, e.AllDay = parseWhen(it.Start)
	e.End, _ = parseWhen(it.End)
	for _, a := range it.Attendees {
		e.Attendees = append(e.Attendees, a.Email)
	}
	return e
}

func parseWhen(d *calendar.EventDateTime) (time.Time, bool) {
	if d == nil {
		return time.Time{}, false
	}
	if d.DateTime != "" {
		t, _ := time.Parse(time.RFC3339, d.DateTime)
		return t, false
	}
	t, _ := time.ParseInLocation("2006-01-02", d.Date, time.Local)
	return t, true
}
