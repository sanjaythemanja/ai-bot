package calendar

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sanjayrj/ai-bot/internal/plugin"
	"github.com/sanjayrj/ai-bot/internal/provider"
)

// Protected time: working hours, working days and blocks (focus time, "no
// meetings Friday afternoon") that slot search skips and that bookings are
// checked against, so nothing lands on top of them unnoticed.

const rulesKey = "calendar.time_rules"

// Block is recurring protected time on some weekdays.
type Block struct {
	Label string   `json:"label"`
	Days  []string `json:"days,omitempty"` // "mon".."sun"; empty = every day
	Start string   `json:"start"`          // "14:00"
	End   string   `json:"end"`            // "16:00"
}

// TimeRules are the user's scheduling rules. Empty fields mean no rule.
type TimeRules struct {
	WorkStart string   `json:"work_start,omitempty"`
	WorkEnd   string   `json:"work_end,omitempty"`
	WorkDays  []string `json:"work_days,omitempty"`
	Protected []Block  `json:"protected,omitempty"`
}

var dayNames = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

func normDay(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) >= 3 {
		for _, d := range dayNames {
			if strings.HasPrefix(s, d) {
				return d, nil
			}
		}
	}
	return "", fmt.Errorf("unknown weekday %q (use mon, tue, … sun)", s)
}

func normDays(in []string) ([]string, error) {
	var out []string
	for _, d := range in {
		n, err := normDay(d)
		if err != nil {
			return nil, err
		}
		if !plugin.ContainsFold(out, n) {
			out = append(out, n)
		}
	}
	return out, nil
}

func validClock(s string) error {
	if _, err := time.Parse("15:04", s); err != nil {
		return fmt.Errorf("time %q must be HH:MM (24-hour)", s)
	}
	return nil
}

// at places an "HH:MM" clock time on day's date.
func at(day time.Time, hhmm string) time.Time {
	c, _ := time.Parse("15:04", hhmm)
	return time.Date(day.Year(), day.Month(), day.Day(), c.Hour(), c.Minute(), 0, 0, day.Location())
}

func weekday(t time.Time) string { return dayNames[t.Weekday()] }

func (r TimeRules) works(t time.Time) bool {
	return len(r.WorkDays) == 0 || plugin.ContainsFold(r.WorkDays, weekday(t))
}

// protectedOn returns the protected blocks on day as time ranges.
func (r TimeRules) protectedOn(day time.Time) []namedRange {
	var out []namedRange
	for _, b := range r.Protected {
		if len(b.Days) > 0 && !plugin.ContainsFold(b.Days, weekday(day)) {
			continue
		}
		out = append(out, namedRange{Label: b.Label, Busy: provider.Busy{Start: at(day, b.Start), End: at(day, b.End)}})
	}
	return out
}

type namedRange struct {
	Label string
	provider.Busy
}

// violations lists the rules a booking from start to end would break.
func (r TimeRules) violations(start, end time.Time) []string {
	var out []string
	if !r.works(start) {
		out = append(out, fmt.Sprintf("On a %s, which isn't one of your working days", start.Format("Monday")))
	}
	if r.WorkStart != "" && r.WorkEnd != "" && (start.Before(at(start, r.WorkStart)) || end.After(at(start, r.WorkEnd))) {
		out = append(out, fmt.Sprintf("Outside your working hours (%s–%s)", r.WorkStart, r.WorkEnd))
	}
	for _, b := range r.protectedOn(start) {
		if start.Before(b.End) && b.Start.Before(end) {
			out = append(out, fmt.Sprintf("Overlaps your protected time %q (%s–%s)", b.Label, b.Start.Format("15:04"), b.End.Format("15:04")))
		}
	}
	return out
}

func (p *Plugin) rules(ctx context.Context) TimeRules {
	var r TimeRules
	_, _ = plugin.LoadSetting(ctx, p.settings, rulesKey, &r)
	return r
}

// warnings explains what a booking would clash with: the user's rules and
// existing events (skipID is the event being moved).
func (p *Plugin) warnings(ctx context.Context, start, end time.Time, skipID string) []string {
	start, end = start.In(p.clock.Loc), end.In(p.clock.Loc)
	out := p.rules(ctx).violations(start, end)
	events, err := p.cal.ListEvents(ctx, "", start, end)
	if err != nil {
		return out
	}
	for _, e := range events {
		if e.ID != skipID && !e.AllDay && e.Start.Before(end) && start.Before(e.End) {
			out = append(out, fmt.Sprintf("Clashes with %q (%s–%s)", e.Title, e.Start.In(p.clock.Loc).Format("15:04"), e.End.In(p.clock.Loc).Format("15:04")))
		}
	}
	return out
}

func withWarnings(summary string, w []string) string {
	if len(w) == 0 {
		return summary
	}
	return summary + " — ⚠ " + strings.Join(w, "; ")
}

func (p *Plugin) ruleTools() []plugin.Tool {
	block := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"label": plugin.Str("Name, e.g. Focus time or No meetings"),
			"days":  plugin.Strs("Weekdays: mon, tue, wed, thu, fri, sat, sun; omit for every day"),
			"start": plugin.Str("Start HH:MM"),
			"end":   plugin.Str("End HH:MM"),
		},
		"required": []string{"label", "start", "end"},
	}
	return []plugin.Tool{
		{
			Name:        "get_time_rules",
			Description: "Show the user's scheduling rules: working hours, working days and protected time (focus blocks, no-meeting times).",
			Properties:  map[string]any{},
			Run:         func(ctx context.Context, _ json.RawMessage) (any, error) { return p.rules(ctx), nil },
		},
		{
			Name:    "set_time_rules",
			Mutates: true,
			Description: "Save the user's scheduling rules, e.g. 'never book before 10am', 'keep Tue/Thu 2–4pm for focus', 'no meetings Friday afternoon'. " +
				"Only the fields you pass change; protected replaces the whole list, so read get_time_rules first and include the blocks to keep. It is a private preference (nothing is sent).",
			Properties: map[string]any{
				"work_start": plugin.Str("Working day start HH:MM"),
				"work_end":   plugin.Str("Working day end HH:MM"),
				"work_days":  plugin.Strs("Working weekdays, e.g. [mon, tue, wed, thu, fri]"),
				"protected":  map[string]any{"type": "array", "items": block, "description": "All protected time blocks"},
			},
			Run: p.setRules,
		},
	}
}

func (p *Plugin) setRules(ctx context.Context, raw json.RawMessage) (any, error) {
	in, err := plugin.Decode[struct {
		WorkStart *string   `json:"work_start"`
		WorkEnd   *string   `json:"work_end"`
		WorkDays  *[]string `json:"work_days"`
		Protected *[]Block  `json:"protected"`
	}](raw)
	if err != nil {
		return nil, err
	}
	if p.settings == nil {
		return nil, errors.New("rules can't be saved in this setup")
	}
	r := p.rules(ctx)
	if in.WorkStart != nil {
		r.WorkStart = strings.TrimSpace(*in.WorkStart)
	}
	if in.WorkEnd != nil {
		r.WorkEnd = strings.TrimSpace(*in.WorkEnd)
	}
	for _, c := range []string{r.WorkStart, r.WorkEnd} {
		if c != "" {
			if err := validClock(c); err != nil {
				return nil, err
			}
		}
	}
	if r.WorkStart != "" && r.WorkEnd != "" && r.WorkEnd <= r.WorkStart {
		return nil, errors.New("work_end must be after work_start")
	}
	if in.WorkDays != nil {
		if r.WorkDays, err = normDays(*in.WorkDays); err != nil {
			return nil, err
		}
	}
	if in.Protected != nil {
		r.Protected = nil
		for _, b := range *in.Protected {
			if err := validClock(b.Start); err != nil {
				return nil, err
			}
			if err := validClock(b.End); err != nil {
				return nil, err
			}
			if b.End <= b.Start {
				return nil, fmt.Errorf("protected block %q must end after it starts", b.Label)
			}
			if b.Days, err = normDays(b.Days); err != nil {
				return nil, err
			}
			r.Protected = append(r.Protected, b)
		}
	}
	if err := p.settings.Save(ctx, rulesKey, r); err != nil {
		return nil, err
	}
	return map[string]any{"status": "rules_saved", "rules": r}, nil
}
