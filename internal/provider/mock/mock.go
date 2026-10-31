// Package mock provides in-memory Mail and Calendar providers seeded with
// sample data, so the app runs end to end without any Google credentials.
// All people and addresses are fictional (example.com).
package mock

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sanjayrj/ai-bot/internal/provider"
)

// Workspace holds all demo data behind one lock.
type Workspace struct {
	mu     sync.Mutex
	emails []provider.Email
	events []provider.Event
	drafts []provider.Draft
	sent   []provider.OutgoingEmail
	others map[string][]provider.Event
	shared map[string]bool
	chans  []provider.Channel
	msgs   []provider.ChatMessage
	seq    int
}

// New returns a seeded demo workspace.
func New(now time.Time) *Workspace {
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	at := func(dayOffset, h, m int) time.Time {
		return day.AddDate(0, 0, dayOffset).Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute)
	}

	w := &Workspace{}
	w.emails = []provider.Email{
		{ID: "m1", ThreadID: "t1", From: "Priya Shah <priya@example.com>", To: "me@example.com", Subject: "Term sheet — need your sign-off by Friday",
			Snippet: "Attached is the revised term sheet. Legal flagged two clauses…", Unread: true, Date: now.Add(-2 * time.Hour),
			Body: "Hi,\n\nAttached is the revised term sheet. Legal flagged two clauses (4.2 liquidation preference and 7.1 board composition) that need your decision. We need sign-off by Friday EOD to keep the close on schedule.\n\nCan we get 30 minutes tomorrow to walk through it?\n\nPriya"},
		{ID: "m2", ThreadID: "t2", From: "Marcus Lee <marcus@example.com>", To: "me@example.com", Subject: "Q3 board deck draft",
			Snippet: "First draft of the board deck is in the shared folder. Revenue slide still…", Unread: true, Date: now.Add(-5 * time.Hour),
			Body: "First draft of the board deck is in the shared folder. The revenue slide still needs the final September numbers from finance. Could you review slides 3–9 before Thursday's prep meeting?\n\nMarcus"},
		{ID: "m3", ThreadID: "t3", From: "Cloud Billing <billing@example.com>", To: "me@example.com", Subject: "Your cloud invoice is available",
			Snippet: "Your invoice for October is now available.", Unread: true, Date: now.Add(-9 * time.Hour),
			Body: "Your invoice for October is now available in the billing console. Amount due: $18,240.11."},
		{ID: "m4", ThreadID: "t4", From: "Dana Kim <dana@example.com>", To: "me@example.com", Subject: "Renewal concerns",
			Snippet: "Before we renew, our team wants to discuss the SSO outages last month…", Unread: true, Date: now.Add(-26 * time.Hour),
			Body: "Hi,\n\nBefore we renew in November, our team wants to discuss the SSO outages last month. Could you or someone from engineering leadership join a call next week?\n\nThanks,\nDana Kim\nVP IT"},
		{ID: "m5", ThreadID: "t5", From: "Weekly Digest <digest@example.com>", To: "me@example.com", Subject: "Top products this week",
			Snippet: "This week's top launches…", Unread: false, Date: now.Add(-30 * time.Hour), Body: "This week's top launches…"},
	}
	w.events = []provider.Event{
		{ID: "e1", Title: "Exec staff sync", Start: at(0, 9, 30), End: at(0, 10, 15), Attendees: []string{"marcus@example.com", "sara@example.com"},
			MeetLink: "https://meet.google.com/abc-defg-hij"},
		{ID: "e2", Title: "1:1 with Sara (VP Eng)", Start: at(0, 11, 0), End: at(0, 11, 30), Attendees: []string{"sara@example.com"},
			MeetLink: "https://meet.google.com/xyz-uvwt-srq"},
		{ID: "e3", Title: "Lunch with investors", Location: "Café Rumi", Start: at(0, 12, 30), End: at(0, 13, 30), Attendees: []string{"priya@example.com"}},
		{ID: "e4", Title: "Hiring debrief — Staff PM", Start: at(0, 15, 0), End: at(0, 15, 45)},
		{ID: "e5", Title: "Board deck prep", Start: at(2, 14, 0), End: at(2, 15, 0), Attendees: []string{"marcus@example.com"}},
		{ID: "e6", Title: "All-hands", Start: at(3, 16, 0), End: at(3, 17, 0)},
	}
	// Colleagues' own meetings that you are not in (visible as free/busy,
	// and in detail only for those who "share" with you).
	w.others = map[string][]provider.Event{
		"sara@example.com": {
			{ID: "s1", Title: "Incident review", Start: at(0, 14, 0), End: at(0, 15, 0), Attendees: []string{"sara@example.com"}},
			{ID: "s2", Title: "Eng hiring sync", Start: at(1, 10, 0), End: at(1, 11, 0), Attendees: []string{"sara@example.com"}},
		},
		"marcus@example.com": {
			{ID: "k1", Title: "Finance close", Start: at(0, 13, 0), End: at(0, 16, 0), Attendees: []string{"marcus@example.com"}},
		},
	}
	w.shared = map[string]bool{"sara@example.com": true} // Marcus shares free/busy only

	w.chans = []provider.Channel{{ID: "C1", Name: "exec-team"}, {ID: "C2", Name: "incidents"}, {ID: "D1", Name: "dm:Sara", IsIM: true}}
	w.msgs = []provider.ChatMessage{
		{Channel: "C2", ChannelName: "incidents", User: "oncall-bot", Text: "SEV2: elevated 5xx on checkout API (eu-west). Mitigation in progress.", TS: "1000.1", Time: now.Add(-40 * time.Minute)},
		{Channel: "C2", ChannelName: "incidents", User: "Sara", Text: "<@me> heads up — this may affect a key customer. Update in 30 min.", TS: "1000.2", Time: now.Add(-35 * time.Minute)},
		{Channel: "C1", ChannelName: "exec-team", User: "Marcus", Text: "<@me> can you confirm the hiring plan numbers for the board deck by Wednesday?", TS: "1000.3", Time: now.Add(-3 * time.Hour)},
		{Channel: "D1", ChannelName: "dm:Sara", User: "Sara", Text: "Can we move our 1:1 to the afternoon?", TS: "1000.4", Time: now.Add(-25 * time.Minute)},
	}
	return w
}

// Meetings returns the Meet view of the workspace.
func (w *Workspace) Meetings() provider.Meetings { return meetView{w} }

// Mail returns the Mail view of the workspace.
func (w *Workspace) Mail() provider.Mail { return mailView{w} }

// Calendar returns the Calendar view of the workspace.
func (w *Workspace) Calendar() provider.Calendar { return calView{w} }

func (w *Workspace) nextID(prefix string) string {
	w.seq++
	return fmt.Sprintf("%s-demo-%d", prefix, w.seq)
}

type mailView struct{ w *Workspace }

func (m mailView) Connected() bool { return true }

func (m mailView) ListMessages(_ context.Context, query string, max int) ([]provider.Email, error) {
	m.w.mu.Lock()
	defer m.w.mu.Unlock()
	q := strings.ToLower(query)
	unreadOnly := strings.Contains(q, "is:unread")
	var terms []string
	for _, t := range strings.Fields(q) {
		if !strings.Contains(t, ":") {
			terms = append(terms, t)
		}
	}
	var out []provider.Email
	for _, e := range m.w.emails {
		if unreadOnly && !e.Unread {
			continue
		}
		hay := strings.ToLower(e.From + " " + e.Subject + " " + e.Body)
		ok := true
		for _, t := range terms {
			if !strings.Contains(hay, t) {
				ok = false
				break
			}
		}
		if ok {
			c := e
			c.Body = ""
			out = append(out, c)
		}
		if max > 0 && len(out) >= max {
			break
		}
	}
	return out, nil
}

func (m mailView) GetMessage(_ context.Context, id string) (provider.Email, error) {
	m.w.mu.Lock()
	defer m.w.mu.Unlock()
	for _, e := range m.w.emails {
		if e.ID == id {
			return e, nil
		}
	}
	return provider.Email{}, fmt.Errorf("message %q not found", id)
}

func (m mailView) CreateDraft(_ context.Context, msg provider.OutgoingEmail) (string, error) {
	m.w.mu.Lock()
	defer m.w.mu.Unlock()
	id := m.w.nextID("draft")
	m.w.drafts = append(m.w.drafts, provider.Draft{ID: id, Message: msg})
	return id, nil
}

func (m mailView) Send(_ context.Context, msg provider.OutgoingEmail) (string, error) {
	m.w.mu.Lock()
	defer m.w.mu.Unlock()
	m.w.sent = append(m.w.sent, msg)
	return m.w.nextID("sent"), nil
}

func (m mailView) ListDrafts(context.Context, int) ([]provider.Draft, error) {
	m.w.mu.Lock()
	defer m.w.mu.Unlock()
	return append([]provider.Draft(nil), m.w.drafts...), nil
}

func (m mailView) findDraft(id string) (int, error) {
	for i, d := range m.w.drafts {
		if d.ID == id {
			return i, nil
		}
	}
	return -1, fmt.Errorf("draft %q not found", id)
}

func (m mailView) GetDraft(_ context.Context, id string) (provider.Draft, error) {
	m.w.mu.Lock()
	defer m.w.mu.Unlock()
	i, err := m.findDraft(id)
	if err != nil {
		return provider.Draft{}, err
	}
	return m.w.drafts[i], nil
}

func (m mailView) UpdateDraft(_ context.Context, id string, msg provider.OutgoingEmail) error {
	m.w.mu.Lock()
	defer m.w.mu.Unlock()
	i, err := m.findDraft(id)
	if err != nil {
		return err
	}
	m.w.drafts[i].Message = msg
	return nil
}

func (m mailView) SendDraft(_ context.Context, id string) (string, error) {
	m.w.mu.Lock()
	defer m.w.mu.Unlock()
	i, err := m.findDraft(id)
	if err != nil {
		return "", err
	}
	m.w.sent = append(m.w.sent, m.w.drafts[i].Message)
	m.w.drafts = append(m.w.drafts[:i], m.w.drafts[i+1:]...)
	return m.w.nextID("sent"), nil
}

func (m mailView) DeleteDraft(_ context.Context, id string) error {
	m.w.mu.Lock()
	defer m.w.mu.Unlock()
	i, err := m.findDraft(id)
	if err != nil {
		return err
	}
	m.w.drafts = append(m.w.drafts[:i], m.w.drafts[i+1:]...)
	return nil
}

// Sent returns emails "sent" in demo mode (for tests).
func (w *Workspace) Sent() []provider.OutgoingEmail {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]provider.OutgoingEmail(nil), w.sent...)
}

type calView struct{ w *Workspace }

func (c calView) Connected() bool { return true }

// calendarOf returns everything on a calendar ("" = yours), and whether its
// details are visible to you.
func (w *Workspace) calendarOf(id string) ([]provider.Event, bool, bool) {
	if id == "" || id == "me" {
		return w.events, true, true
	}
	others, known := w.others[id]
	if !known && !strings.HasSuffix(id, "@example.com") {
		return nil, false, false
	}
	evs := append([]provider.Event(nil), others...)
	for _, e := range w.events { // meetings they share with you
		if containsAddr(e.Attendees, id) {
			evs = append(evs, e)
		}
	}
	return evs, true, w.shared[id]
}

func containsAddr(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

func (c calView) ListEvents(_ context.Context, calendarID string, from, to time.Time) ([]provider.Event, error) {
	c.w.mu.Lock()
	defer c.w.mu.Unlock()
	evs, ok, visible := c.w.calendarOf(calendarID)
	if !ok || !visible {
		return nil, fmt.Errorf("calendar %s: not shared with you (ask them to share 'See all event details', or use check_availability for free/busy)", calendarID)
	}
	var out []provider.Event
	for _, e := range evs {
		if e.End.After(from) && e.Start.Before(to) {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out, nil
}

func (c calView) FreeBusy(_ context.Context, ids []string, from, to time.Time) (map[string]provider.Availability, error) {
	c.w.mu.Lock()
	defer c.w.mu.Unlock()
	out := map[string]provider.Availability{}
	for _, id := range ids {
		evs, ok, _ := c.w.calendarOf(id)
		if !ok {
			out[id] = provider.Availability{Error: "calendar not found or not visible to you (outside your Workspace, or sharing is off)"}
			continue
		}
		a := provider.Availability{Busy: []provider.Busy{}}
		for _, e := range evs {
			if e.End.After(from) && e.Start.Before(to) {
				a.Busy = append(a.Busy, provider.Busy{Start: e.Start, End: e.End})
			}
		}
		sort.Slice(a.Busy, func(i, j int) bool { return a.Busy[i].Start.Before(a.Busy[j].Start) })
		out[id] = a
	}
	return out, nil
}

func (c calView) CreateEvent(_ context.Context, ev provider.NewEvent) (provider.Event, error) {
	c.w.mu.Lock()
	defer c.w.mu.Unlock()
	e := provider.Event{ID: c.w.nextID("evt"), Title: ev.Title, Description: ev.Description, Location: ev.Location,
		Start: ev.Start, End: ev.End, Attendees: ev.Attendees}
	c.w.events = append(c.w.events, e)
	return e, nil
}

func (c calView) find(id string) (int, error) {
	for i, e := range c.w.events {
		if e.ID == id {
			return i, nil
		}
	}
	return -1, fmt.Errorf("event %q not found", id)
}

func (c calView) GetEvent(_ context.Context, calendarID, id string) (provider.Event, error) {
	c.w.mu.Lock()
	defer c.w.mu.Unlock()
	evs, ok, visible := c.w.calendarOf(calendarID)
	if !ok || !visible {
		return provider.Event{}, fmt.Errorf("calendar %s: not shared with you", calendarID)
	}
	for _, e := range evs {
		if e.ID == id {
			return e, nil
		}
	}
	return provider.Event{}, fmt.Errorf("event %q not found", id)
}

func (c calView) UpdateEvent(_ context.Context, id string, upd provider.EventUpdate) (provider.Event, error) {
	c.w.mu.Lock()
	defer c.w.mu.Unlock()
	i, err := c.find(id)
	if err != nil {
		return provider.Event{}, err
	}
	e := &c.w.events[i]
	if upd.Title != nil {
		e.Title = *upd.Title
	}
	if upd.Description != nil {
		e.Description = *upd.Description
	}
	if upd.Location != nil {
		e.Location = *upd.Location
	}
	if upd.Start != nil {
		e.Start = *upd.Start
	}
	if upd.End != nil {
		e.End = *upd.End
	}
	if upd.Attendees != nil {
		e.Attendees = append([]string(nil), (*upd.Attendees)...)
	}
	return *e, nil
}

func (c calView) DeleteEvent(_ context.Context, id string) error {
	c.w.mu.Lock()
	defer c.w.mu.Unlock()
	i, err := c.find(id)
	if err != nil {
		return err
	}
	c.w.events = append(c.w.events[:i], c.w.events[i+1:]...)
	return nil
}

type meetView struct{ w *Workspace }

func (m meetView) Connected() bool { return true }

// Transcript returns a sample transcript for the demo exec staff sync.
func (m meetView) Transcript(_ context.Context, code string) (provider.Transcript, error) {
	if code != "abc-defg-hij" {
		return provider.Transcript{MeetingCode: code}, provider.ErrNoTranscript
	}
	m.w.mu.Lock()
	start := m.w.events[0].Start
	m.w.mu.Unlock()
	line := func(min int, who, text string) provider.TranscriptLine {
		return provider.TranscriptLine{Speaker: who, Time: start.Add(time.Duration(min) * time.Minute), Text: text}
	}
	return provider.Transcript{MeetingCode: code, Lines: []provider.TranscriptLine{
		line(0, "Marcus Lee", "Board deck: slides 3 to 9 are drafted, revenue still waits on September numbers."),
		line(2, "Lena (CFO)", "September close is done. I'll send final numbers today."),
		line(5, "Sara (VP Eng)", "SSO outage postmortem is finished. Root cause was an expired cert; we've added monitoring."),
		line(8, "Sara (VP Eng)", "BigCustomer wants an exec on a call before renewal. I can join, but we need the CEO too."),
		line(11, "Marcus Lee", "Decision: CEO joins the BigCustomer call next week. Marcus owns scheduling."),
		line(13, "Lena (CFO)", "Action: Lena sends numbers to Marcus by Wednesday so the deck is final Thursday."),
	}}, nil
}

// Chat returns the Slack view of the workspace.
func (w *Workspace) Chat() provider.Chat { return chatView{w} }

// Posted returns messages "posted" by the user in demo mode (for tests).
func (w *Workspace) Posted() []provider.ChatMessage {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []provider.ChatMessage
	for _, m := range w.msgs {
		if m.User == "you" {
			out = append(out, m)
		}
	}
	return out
}

type chatView struct{ w *Workspace }

func (c chatView) ListChannels(context.Context) ([]provider.Channel, error) {
	c.w.mu.Lock()
	defer c.w.mu.Unlock()
	return append([]provider.Channel(nil), c.w.chans...), nil
}

func (c chatView) channel(name string) (provider.Channel, error) {
	name = strings.TrimPrefix(strings.TrimSpace(name), "#")
	for _, ch := range c.w.chans {
		if ch.ID == name || strings.EqualFold(ch.Name, name) {
			return ch, nil
		}
	}
	return provider.Channel{}, fmt.Errorf("no channel %q among the user's channels", name)
}

func (c chatView) History(_ context.Context, channel string, limit int) ([]provider.ChatMessage, error) {
	c.w.mu.Lock()
	defer c.w.mu.Unlock()
	ch, err := c.channel(channel)
	if err != nil {
		return nil, err
	}
	var out []provider.ChatMessage
	for i := len(c.w.msgs) - 1; i >= 0; i-- {
		if c.w.msgs[i].Channel == ch.ID {
			out = append(out, c.w.msgs[i])
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (c chatView) Search(_ context.Context, query string, limit int) ([]provider.ChatMessage, error) {
	c.w.mu.Lock()
	defer c.w.mu.Unlock()
	var out []provider.ChatMessage
	for _, m := range c.w.msgs {
		if strings.Contains(strings.ToLower(m.Text+" "+m.User), strings.ToLower(query)) {
			out = append(out, m)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (c chatView) Mentions(ctx context.Context, limit int) ([]provider.ChatMessage, error) {
	return c.Search(ctx, "<@me>", limit)
}

func (c chatView) Post(_ context.Context, channel, text, threadTS string) (string, error) {
	c.w.mu.Lock()
	defer c.w.mu.Unlock()
	ch, err := c.channel(channel)
	if err != nil {
		return "", err
	}
	ts := c.w.nextID("ts")
	c.w.msgs = append(c.w.msgs, provider.ChatMessage{Channel: ch.ID, ChannelName: ch.Name, User: "you", Text: text, TS: ts, ThreadTS: threadTS, Time: time.Now()})
	return ts, nil
}
