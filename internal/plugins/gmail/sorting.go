package gmail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/sanjayrj/ai-bot/internal/plugin"
	"github.com/sanjayrj/ai-bot/internal/provider"
)

// Explainable sorting: every email gets a priority and the reasons for it,
// and the user's corrections are saved as rules that always win.

const sortingKey = "gmail.sorting_rules"

// SortRule pins a priority for matching mail. Match is a sender address, an
// @domain, or a word found in the sender name or subject.
type SortRule struct {
	Match    string `json:"match"`
	Priority string `json:"priority"` // "important" or "low"
}

func (r SortRule) matches(e provider.Email) bool {
	m := strings.ToLower(strings.TrimSpace(r.Match))
	from := plugin.Addr(e.From)
	switch {
	case m == "":
		return false
	case strings.HasPrefix(m, "@"):
		return strings.HasSuffix(from, m)
	case strings.Contains(m, "@"):
		return from == m
	default:
		return strings.Contains(strings.ToLower(e.From), m) || strings.Contains(strings.ToLower(e.Subject), m)
	}
}

var (
	deadlineRe = regexp.MustCompile(`(?i)\b(urgent|asap|deadline|overdue|final notice|action required|sign[- ]off|due (today|tomorrow|by \w+)|by (today|tonight|tomorrow|monday|tuesday|wednesday|thursday|friday|eod|end of (the )?(day|week)))\b`)
	bulkWords  = []string{"newsletter", "digest", "marketing", "promo", "news", "updates", "hello", "info"}
)

func bulkSender(addr string) bool {
	if automated(addr) {
		return true
	}
	local, _, _ := strings.Cut(addr, "@")
	for _, w := range bulkWords {
		if local == w || strings.HasPrefix(local, w+"-") || strings.HasPrefix(local, w+".") {
			return true
		}
	}
	return false
}

// verdict ranks one email. Rules win; otherwise signals add up.
func verdict(e provider.Email, rules []SortRule, contacts map[string]bool) plugin.Verdict {
	for _, r := range rules {
		if r.matches(e) {
			return plugin.Verdict{Priority: r.Priority, Reasons: []string{fmt.Sprintf("Your rule: %q is always %s", r.Match, r.Priority)}}
		}
	}
	from := plugin.Addr(e.From)
	text := e.Subject + " " + e.Snippet
	score := 0
	var reasons []string
	if m := deadlineRe.FindString(text); m != "" {
		score += 2
		reasons = append(reasons, fmt.Sprintf("Mentions a deadline (%q)", strings.ToLower(m)))
	}
	if contacts[from] {
		score++
		reasons = append(reasons, "You've emailed "+from+" recently")
	}
	if strings.Contains(e.Snippet, "?") {
		score++
		reasons = append(reasons, "Asks you a question")
	}
	if bulkSender(from) {
		score -= 2
		reasons = append(reasons, "Sent by an automated or newsletter address ("+from+")")
	}
	v := plugin.Verdict{Priority: "normal", Reasons: reasons}
	switch {
	case score >= 2:
		v.Priority = "important"
	case score < 0:
		v.Priority = "low"
	}
	if len(v.Reasons) == 0 {
		v.Reasons = []string{"Nothing stands out: not from a frequent contact, no deadline or question"}
	}
	return v
}

func (p *Plugin) rules(ctx context.Context) []SortRule {
	var rules []SortRule
	_, _ = plugin.LoadSetting(ctx, p.settings, sortingKey, &rules)
	return rules
}

// contacts are the people the user emailed in the last 30 days.
func (p *Plugin) contacts(ctx context.Context) map[string]bool {
	out := map[string]bool{}
	sent, err := p.mail.ListMessages(ctx, "in:sent newer_than:30d", 25)
	if err != nil {
		return out
	}
	for _, m := range sent {
		for _, a := range plugin.Addrs(m.To) {
			out[a] = true
		}
	}
	return out
}

// SortEmails implements plugin.Sorter.
func (p *Plugin) SortEmails(ctx context.Context, emails []provider.Email) (map[string]plugin.Verdict, error) {
	rules, contacts := p.rules(ctx), p.contacts(ctx)
	out := make(map[string]plugin.Verdict, len(emails))
	for _, e := range emails {
		out[e.ID] = verdict(e, rules, contacts)
	}
	return out, nil
}

type sortedEmail struct {
	provider.Email
	plugin.Verdict
}

var rank = map[string]int{"important": 0, "normal": 1, "low": 2}

func (p *Plugin) saveRules(ctx context.Context, rules []SortRule) error {
	if p.settings == nil {
		return errors.New("rules can't be saved in this setup")
	}
	return p.settings.Save(ctx, sortingKey, rules)
}

func (p *Plugin) sortingTools() []plugin.Tool {
	match := plugin.Str("Sender email (priya@acme.com), a domain (@acme.com), or a word in the sender name or subject")
	return []plugin.Tool{
		{
			Name: "triage_inbox",
			Description: "Sort email into important / normal / low, with the reasons for each (deadlines, people the user writes to, questions, newsletters) and the user's own sorting rules applied. " +
				"Prefer this over search_emails for 'what needs my attention?'. Always pass on the reasons.",
			Properties: map[string]any{
				"query":       plugin.Str("Gmail query, default 'is:unread in:inbox newer_than:3d'"),
				"max_results": plugin.Num("Max messages, default 20"),
			},
			Run: func(ctx context.Context, raw json.RawMessage) (any, error) {
				in, err := plugin.Decode[struct {
					Query string `json:"query"`
					Max   int    `json:"max_results"`
				}](raw)
				if err != nil {
					return nil, err
				}
				if in.Query == "" {
					in.Query = "is:unread in:inbox newer_than:3d"
				}
				if in.Max <= 0 {
					in.Max = 20
				}
				emails, err := p.mail.ListMessages(ctx, in.Query, in.Max)
				if err != nil {
					return nil, err
				}
				verdicts, _ := p.SortEmails(ctx, emails)
				out := make([]sortedEmail, 0, len(emails))
				for _, e := range emails {
					out = append(out, sortedEmail{e, verdicts[e.ID]})
				}
				sort.SliceStable(out, func(i, j int) bool { return rank[out[i].Priority] < rank[out[j].Priority] })
				return out, nil
			},
		},
		{
			Name:        "list_sorting_rules",
			Description: "List the user's email sorting rules (senders, domains or words that are always important or always low priority).",
			Properties:  map[string]any{},
			Run: func(ctx context.Context, _ json.RawMessage) (any, error) {
				return append([]SortRule{}, p.rules(ctx)...), nil
			},
		},
		{
			Name:    "add_sorting_rule",
			Mutates: true,
			Description: "Remember a sorting correction, e.g. 'emails from Acme are always important' or 'LinkedIn is low priority'. " +
				"It is a private preference (nothing is sent) and applies from now on. Replaces an existing rule with the same match.",
			Properties: map[string]any{"match": match, "priority": map[string]any{"type": "string", "enum": []string{"important", "low"}, "description": "Priority to always give matching mail"}},
			Required:   []string{"match", "priority"},
			Run: func(ctx context.Context, raw json.RawMessage) (any, error) {
				in, err := plugin.Decode[SortRule](raw)
				if err != nil {
					return nil, err
				}
				in.Match = strings.TrimSpace(in.Match)
				if in.Match == "" || (in.Priority != "important" && in.Priority != "low") {
					return nil, errors.New("match is required and priority must be important or low")
				}
				rules := []SortRule{in}
				for _, r := range p.rules(ctx) {
					if !strings.EqualFold(r.Match, in.Match) {
						rules = append(rules, r)
					}
				}
				if err := p.saveRules(ctx, rules); err != nil {
					return nil, err
				}
				return map[string]any{"status": "rule_saved", "rule": in, "rules": rules}, nil
			},
		},
		{
			Name:        "remove_sorting_rule",
			Mutates:     true,
			Description: "Delete one of the user's email sorting rules.",
			Properties:  map[string]any{"match": match},
			Required:    []string{"match"},
			Run: func(ctx context.Context, raw json.RawMessage) (any, error) {
				in, err := plugin.Decode[SortRule](raw)
				if err != nil {
					return nil, err
				}
				var rules []SortRule
				found := false
				for _, r := range p.rules(ctx) {
					if strings.EqualFold(r.Match, strings.TrimSpace(in.Match)) {
						found = true
						continue
					}
					rules = append(rules, r)
				}
				if !found {
					return nil, fmt.Errorf("no sorting rule matches %q", in.Match)
				}
				if err := p.saveRules(ctx, rules); err != nil {
					return nil, err
				}
				return map[string]any{"status": "rule_removed", "rules": append([]SortRule{}, rules...)}, nil
			},
		},
	}
}
