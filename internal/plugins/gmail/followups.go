package gmail

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/sanjayrj/ai-bot/internal/plugin"
)

// Follow-up tracking: threads where the user wrote last and nobody has
// answered, and promises the user made in sent mail.

// awaiting is a sent email still waiting for a reply.
type awaiting struct {
	ThreadID    string `json:"thread_id"`
	MessageID   string `json:"message_id"` // reply_to_id for a nudge
	To          string `json:"to"`
	Subject     string `json:"subject"`
	SentAt      string `json:"sent_at"`
	DaysWaiting int    `json:"days_waiting"`
	Snippet     string `json:"snippet,omitempty"`
}

// commitment is a promise found in a sent email.
type commitment struct {
	MessageID string   `json:"message_id"`
	ThreadID  string   `json:"thread_id"`
	To        string   `json:"to"`
	Subject   string   `json:"subject"`
	SentAt    string   `json:"sent_at"`
	DaysAgo   int      `json:"days_ago"`
	Promises  []string `json:"promises"`
}

// automated senders never answer, so don't wait on them.
func automated(addr string) bool {
	local, _, _ := strings.Cut(addr, "@")
	for _, w := range []string{"noreply", "no-reply", "donotreply", "do-not-reply", "notifications", "mailer-daemon"} {
		if strings.Contains(local, w) {
			return true
		}
	}
	return false
}

func daysSince(t, now time.Time) int { return int(now.Sub(t).Hours() / 24) }

// awaitingReplies lists sent threads with no reply after minDays (0 = any
// age, including mail sent minutes ago), newest first.
func (p *Plugin) awaitingReplies(ctx context.Context, minDays, lookback int, now time.Time) ([]awaiting, error) {
	if minDays < 0 {
		minDays = 0
	}
	if lookback <= 0 || lookback > 60 {
		lookback = 14
	}
	sent, err := p.mail.ListMessages(ctx, fmt.Sprintf("in:sent newer_than:%dd", lookback), 30)
	if err != nil {
		return nil, err
	}
	out := []awaiting{}
	seen := map[string]bool{}
	checked := 0
	for _, m := range sent {
		if seen[m.ThreadID] || checked >= 20 { // each thread costs one API call
			continue
		}
		seen[m.ThreadID] = true
		to := plugin.Addrs(m.To)
		if len(to) == 0 || automated(to[0]) {
			continue
		}
		checked++
		thread, err := p.mail.Thread(ctx, m.ThreadID)
		if err != nil || len(thread) == 0 {
			continue
		}
		last := thread[len(thread)-1]
		if !last.Sent || daysSince(last.Date, now) < minDays {
			continue
		}
		out = append(out, awaiting{
			ThreadID: last.ThreadID, MessageID: last.ID, To: last.To, Subject: last.Subject,
			SentAt: last.Date.Format(time.RFC3339), DaysWaiting: daysSince(last.Date, now), Snippet: last.Snippet,
		})
	}
	return out, nil
}

// commitments finds promises in the user's recent sent mail.
func (p *Plugin) commitments(ctx context.Context, lookback int, now time.Time) ([]commitment, error) {
	if lookback <= 0 || lookback > 60 {
		lookback = 14
	}
	sent, err := p.mail.ListMessages(ctx, fmt.Sprintf("in:sent newer_than:%dd", lookback), 15)
	if err != nil {
		return nil, err
	}
	out := []commitment{}
	for _, m := range sent {
		full, err := p.mail.GetMessage(ctx, m.ID)
		if err != nil {
			continue
		}
		promises := plugin.Promises(plugin.StripQuoted(full.Body), 3)
		if len(promises) == 0 {
			continue
		}
		out = append(out, commitment{
			MessageID: m.ID, ThreadID: m.ThreadID, To: m.To, Subject: m.Subject,
			SentAt: m.Date.Format(time.RFC3339), DaysAgo: daysSince(m.Date, now), Promises: promises,
		})
	}
	return out, nil
}

func (p *Plugin) followUpTools() []plugin.Tool {
	return []plugin.Tool{
		{
			Name: "find_awaiting_replies",
			Description: "Find emails the user sent that nobody has answered yet (the user wrote last in the thread). " +
				"Use for 'who hasn't replied?', 'what am I waiting on?' and follow-up nudges. To nudge, send_email with reply_to_id set to message_id.",
			Properties: map[string]any{
				"min_days":      plugin.Num("Only threads quiet for at least this many days, default 2. Use 0 to include mail sent today (e.g. 'who hasn't replied today?')"),
				"lookback_days": plugin.Num("How far back to look in sent mail, default 14"),
			},
			Run: func(ctx context.Context, raw json.RawMessage) (any, error) {
				in, err := plugin.Decode[struct {
					MinDays  *int `json:"min_days"`
					Lookback int  `json:"lookback_days"`
				}](raw)
				if err != nil {
					return nil, err
				}
				minDays := 2
				if in.MinDays != nil {
					minDays = *in.MinDays
				}
				return p.awaitingReplies(ctx, minDays, in.Lookback, time.Now())
			},
		},
		{
			Name: "find_my_commitments",
			Description: "Find promises the user made in sent email (\"I'll send the deck by Friday\"), with when and to whom. " +
				"Work out each promise's due date from sent_at and the wording, and say which are due soon or overdue. Check the thread before claiming one is done or not.",
			Properties: map[string]any{"lookback_days": plugin.Num("How far back to look in sent mail, default 14")},
			Run: func(ctx context.Context, raw json.RawMessage) (any, error) {
				in, err := plugin.Decode[struct {
					Lookback int `json:"lookback_days"`
				}](raw)
				if err != nil {
					return nil, err
				}
				return p.commitments(ctx, in.Lookback, time.Now())
			},
		},
	}
}
