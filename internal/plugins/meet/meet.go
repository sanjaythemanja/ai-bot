// Package meet is the Google Meet plugin: read-only access to transcripts of
// past meetings so the model can summarize them. It has no writes, so it
// implements only plugin.Plugin and plugin.Guide.
package meet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/sanjayrj/ai-bot/internal/plugin"
	"github.com/sanjayrj/ai-bot/internal/provider"
)

type Plugin struct {
	meet  provider.Meetings
	cal   provider.Calendar // to find a meeting's Meet link from its event
	clock plugin.Clock
}

func New(meet provider.Meetings, cal provider.Calendar, loc *time.Location) *Plugin {
	return &Plugin{meet: meet, cal: cal, clock: plugin.Clock{Loc: loc}}
}

var _ plugin.Guide = (*Plugin)(nil)

func (p *Plugin) Name() string { return "meet" }

func (p *Plugin) Guidance() string {
	return `- To summarize a past meeting, call get_meeting_transcript and report what was discussed, decisions, action items with owners, and open questions. If there is no transcript, say so and summarize only the invite details.`
}

func (p *Plugin) Tools() []plugin.Tool {
	return []plugin.Tool{{
		Name: "get_meeting_transcript",
		Description: "Get the transcript of a past Google Meet meeting, to summarize what was discussed, decisions and action items. " +
			"Pass the event_id (and calendar if it's a colleague's event) or a meeting_code like abc-defg-hij. " +
			"Only works if the meeting was transcribed and the user organized it or was given access.",
		Properties: map[string]any{
			"event_id":     plugin.Str("Calendar event ID of the meeting"),
			"calendar":     plugin.Str("Colleague's email if the event is on their calendar"),
			"meeting_code": plugin.Str("Google Meet code, if known instead of an event"),
		},
		Run: p.transcript,
	}}
}

func (p *Plugin) transcript(ctx context.Context, raw json.RawMessage) (any, error) {
	in, err := plugin.Decode[struct {
		EventID     string `json:"event_id"`
		Calendar    string `json:"calendar"`
		MeetingCode string `json:"meeting_code"`
	}](raw)
	if err != nil {
		return nil, err
	}
	if p.meet == nil {
		return nil, provider.ErrNotConnected
	}
	out := map[string]any{}
	code := in.MeetingCode
	if in.EventID != "" {
		ev, err := p.cal.GetEvent(ctx, in.Calendar, in.EventID)
		if err != nil {
			return nil, err
		}
		out["meeting"] = map[string]any{"title": ev.Title, "start": p.clock.Short(ev.Start), "attendees": ev.Attendees}
		if code == "" {
			code = meetCode(ev.MeetLink)
		}
	}
	if code == "" {
		return nil, errors.New("this meeting has no Google Meet link, so there is no transcript; only its invite details are available")
	}
	tr, err := p.meet.Transcript(ctx, code)
	if errors.Is(err, provider.ErrNoTranscript) {
		return nil, fmt.Errorf("%w: it may not have been transcribed, may still be processing, or the user isn't the organizer", err)
	}
	if err != nil {
		return nil, err
	}
	lines := make([]string, 0, len(tr.Lines))
	for _, l := range tr.Lines {
		lines = append(lines, fmt.Sprintf("[%s] %s: %s", l.Time.In(p.clock.Loc).Format("15:04"), l.Speaker, l.Text))
	}
	out["meeting_code"], out["transcript"] = code, strings.Join(lines, "\n")
	if tr.Truncated {
		out["note"] = "Transcript truncated to fit; summarize what is shown and say it was cut."
	}
	return out, nil
}

// meetCode extracts "abc-defg-hij" from a Meet URL.
func meetCode(link string) string {
	u, err := url.Parse(link)
	if err != nil || !strings.HasSuffix(u.Host, "meet.google.com") {
		return ""
	}
	return strings.Trim(u.Path, "/")
}
