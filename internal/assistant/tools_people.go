package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/sanjayrj/ai-bot/internal/provider"
)

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

func (t *Toolbox) registerPeople() {
	people := strs(`Email addresses of colleagues; use "me" for the user`)

	t.add("check_availability",
		"Check whether people are free or busy in a time range, using Google free/busy. Works for colleagues in the same Workspace without them sharing their calendar (shows busy times, not meeting details).",
		prop{"people": people, "start": str("Range start, RFC 3339"), "end": str("Range end, RFC 3339")},
		[]string{"people", "start", "end"},
		func(ctx context.Context, raw json.RawMessage) (any, error) {
			in, err := decode[struct {
				People     []string
				Start, End string
			}](raw)
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
			if !to.After(from) {
				return nil, errors.New("end must be after start")
			}
			res, err := t.cal.FreeBusy(ctx, people0(in.People), from, to)
			if err != nil {
				return nil, err
			}
			out := make([]busyView, 0, len(res))
			for _, p := range people0(in.People) {
				a := res[p]
				v := busyView{Person: p, Error: a.Error}
				if a.Error == "" {
					free := len(a.Busy) == 0
					v.Free = &free
					for _, b := range a.Busy {
						v.Busy = append(v.Busy, busyWindow{t.fmtTime(b.Start), t.fmtTime(b.End)})
					}
				}
				out = append(out, v)
			}
			return out, nil
		})

	t.add("find_common_free_slots",
		"Find times on a day when the user and all the given people are free (Google free/busy), e.g. to schedule a meeting. People whose calendars can't be read are listed in unknown and NOT counted as free.",
		prop{
			"people":           people,
			"date":             str("Day to search, YYYY-MM-DD"),
			"duration_minutes": num("Required slot length in minutes"),
			"day_start":        str("Working day start HH:MM, default 09:00"),
			"day_end":          str("Working day end HH:MM, default 18:00"),
		},
		[]string{"people", "date", "duration_minutes"},
		func(ctx context.Context, raw json.RawMessage) (any, error) {
			in, err := decode[struct {
				People   []string `json:"people"`
				Date     string   `json:"date"`
				Duration int      `json:"duration_minutes"`
				DayStart string   `json:"day_start"`
				DayEnd   string   `json:"day_end"`
			}](raw)
			if err != nil {
				return nil, err
			}
			if in.Duration <= 0 {
				in.Duration = 30
			}
			from, to, err := t.dayWindow(in.Date, in.DayStart, in.DayEnd)
			if err != nil {
				return nil, err
			}
			ids := people0(append([]string{"me"}, in.People...))
			res, err := t.cal.FreeBusy(ctx, ids, from, to)
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
			out := map[string]any{"slots": gaps(from, to, busy, time.Duration(in.Duration)*time.Minute), "checked": ids}
			if len(unknown) > 0 {
				out["unknown"] = unknown
				out["note"] = "Some calendars could not be read; these slots only account for the others."
			}
			return out, nil
		})

	t.add("get_meeting_transcript",
		"Get the transcript of a past Google Meet meeting, to summarize what was discussed, decisions and action items. "+
			"Pass the event_id (and calendar if it's a colleague's event) or a meeting_code like abc-defg-hij. "+
			"Only works if the meeting was transcribed and the user organized it or was given access.",
		prop{
			"event_id":     str("Calendar event ID of the meeting"),
			"calendar":     str("Colleague's email if the event is on their calendar"),
			"meeting_code": str("Google Meet code, if known instead of an event"),
		},
		nil,
		func(ctx context.Context, raw json.RawMessage) (any, error) {
			in, err := decode[struct {
				EventID     string `json:"event_id"`
				Calendar    string `json:"calendar"`
				MeetingCode string `json:"meeting_code"`
			}](raw)
			if err != nil {
				return nil, err
			}
			if t.meet == nil {
				return nil, provider.ErrNotConnected
			}
			out := map[string]any{}
			code := in.MeetingCode
			if in.EventID != "" {
				ev, err := t.cal.GetEvent(ctx, in.Calendar, in.EventID)
				if err != nil {
					return nil, err
				}
				out["meeting"] = map[string]any{"title": ev.Title, "start": t.fmtTime(ev.Start), "attendees": ev.Attendees}
				if code == "" {
					code = meetCode(ev.MeetLink)
				}
			}
			if code == "" {
				return nil, errors.New("this meeting has no Google Meet link, so there is no transcript; only its invite details are available")
			}
			tr, err := t.meet.Transcript(ctx, code)
			if errors.Is(err, provider.ErrNoTranscript) {
				return nil, fmt.Errorf("%w: it may not have been transcribed, may still be processing, or the user isn't the organizer", err)
			}
			if err != nil {
				return nil, err
			}
			lines := make([]string, 0, len(tr.Lines))
			for _, l := range tr.Lines {
				lines = append(lines, fmt.Sprintf("[%s] %s: %s", l.Time.In(t.loc).Format("15:04"), l.Speaker, l.Text))
			}
			out["meeting_code"], out["transcript"] = code, strings.Join(lines, "\n")
			if tr.Truncated {
				out["note"] = "Transcript truncated to fit; summarize what is shown and say it was cut."
			}
			return out, nil
		})
}

// meetCode extracts "abc-defg-hij" from a Meet URL.
func meetCode(link string) string {
	u, err := url.Parse(link)
	if err != nil || !strings.HasSuffix(u.Host, "meet.google.com") {
		return ""
	}
	return strings.Trim(u.Path, "/")
}

// people0 trims and de-duplicates the list.
func people0(in []string) []string {
	var out []string
	for _, p := range in {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" && !containsFold(out, p) {
			out = append(out, p)
		}
	}
	return out
}
