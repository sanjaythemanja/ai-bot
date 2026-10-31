package google

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"google.golang.org/api/googleapi"
	"google.golang.org/api/meet/v2"
	"google.golang.org/api/option"

	"github.com/sanjayrj/ai-bot/internal/provider"
)

// Meet implements provider.Meetings with the Google Meet REST API.
type Meet struct{ auth *Auth }

// NewMeet returns a Meet provider backed by auth.
func NewMeet(auth *Auth) *Meet { return &Meet{auth: auth} }

var _ provider.Meetings = (*Meet)(nil)

func (m *Meet) Connected() bool { return m.auth.Connected() }

// maxTranscriptChars keeps a long meeting within the model's budget.
const maxTranscriptChars = 60000

func (m *Meet) Transcript(ctx context.Context, meetingCode string) (provider.Transcript, error) {
	hc, err := m.auth.HTTPClient(ctx)
	if err != nil {
		return provider.Transcript{}, err
	}
	s, err := meet.NewService(ctx, option.WithHTTPClient(hc))
	if err != nil {
		return provider.Transcript{}, err
	}
	out := provider.Transcript{MeetingCode: meetingCode}

	recs, err := s.ConferenceRecords.List().Filter(fmt.Sprintf(`space.meeting_code = "%s"`, meetingCode)).Context(ctx).Do()
	if err != nil {
		return out, meetErr(err)
	}
	names := map[string]string{} // participant resource -> display name
	size := 0
	for _, rec := range recs.ConferenceRecords {
		ts, err := s.ConferenceRecords.Transcripts.List(rec.Name).Context(ctx).Do()
		if err != nil {
			return out, meetErr(err)
		}
		for _, tr := range ts.Transcripts {
			token := ""
			for {
				call := s.ConferenceRecords.Transcripts.Entries.List(tr.Name).PageSize(100).Context(ctx)
				if token != "" {
					call = call.PageToken(token)
				}
				page, err := call.Do()
				if err != nil {
					return out, meetErr(err)
				}
				for _, e := range page.TranscriptEntries {
					line := provider.TranscriptLine{Speaker: m.speaker(ctx, s, names, e.Participant), Text: e.Text}
					line.Time, _ = time.Parse(time.RFC3339, e.StartTime)
					size += len(line.Text) + len(line.Speaker)
					if size > maxTranscriptChars {
						out.Truncated = true
						break
					}
					out.Lines = append(out.Lines, line)
				}
				if out.Truncated || page.NextPageToken == "" {
					break
				}
				token = page.NextPageToken
			}
		}
	}
	if len(out.Lines) == 0 {
		return out, provider.ErrNoTranscript
	}
	sort.SliceStable(out.Lines, func(i, j int) bool { return out.Lines[i].Time.Before(out.Lines[j].Time) })
	return out, nil
}

func (m *Meet) speaker(ctx context.Context, s *meet.Service, cache map[string]string, name string) string {
	if n, ok := cache[name]; ok {
		return n
	}
	n := "Unknown"
	if p, err := s.ConferenceRecords.Participants.Get(name).Context(ctx).Do(); err == nil {
		switch {
		case p.SignedinUser != nil && p.SignedinUser.DisplayName != "":
			n = p.SignedinUser.DisplayName
		case p.AnonymousUser != nil && p.AnonymousUser.DisplayName != "":
			n = p.AnonymousUser.DisplayName
		case p.PhoneUser != nil && p.PhoneUser.DisplayName != "":
			n = p.PhoneUser.DisplayName
		}
	}
	cache[name] = n
	return n
}

// meetErr turns common API failures into actionable messages.
func meetErr(err error) error {
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		msg := strings.ToLower(gerr.Message)
		switch {
		case gerr.Code == http.StatusForbidden && strings.Contains(msg, "scope"):
			return fmt.Errorf("Google hasn't granted transcript access yet: disconnect and reconnect Google in the app: %w", err)
		case gerr.Code == http.StatusForbidden && (strings.Contains(msg, "has not been used") || strings.Contains(msg, "disabled")):
			return fmt.Errorf("enable the Google Meet REST API in Google Cloud Console for this project: %w", err)
		case gerr.Code == http.StatusForbidden:
			return fmt.Errorf("no access to this meeting's records (only the organizer and people it's shared with can read transcripts): %w", err)
		}
	}
	return fmt.Errorf("meet: %w", err)
}
