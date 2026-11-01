package gmail

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sanjayrj/ai-bot/internal/plugin"
	"github.com/sanjayrj/ai-bot/internal/provider"
)

// Writing style: learned from the user's own sent mail so drafts sound like
// them, with the source emails listed so they can see what it learned from.

const styleKey = "gmail.writing_style"

// styleSource is one sent email the style was learned from.
type styleSource struct {
	Subject string `json:"subject"`
	To      string `json:"to"`
	SentAt  string `json:"sent_at"`
}

type writingStyle struct {
	LearnedAt time.Time     `json:"learned_at"`
	Greetings []string      `json:"greetings"` // most used first, e.g. "Hi {name},"
	SignOffs  []string      `json:"sign_offs"`
	AvgWords  int           `json:"avg_words"`
	Samples   []string      `json:"samples"` // short excerpts of real emails
	Sources   []styleSource `json:"sources"`
}

// styleNote rides along on an email action so the preview can say which
// emails the wording was modelled on.
type styleNote struct {
	Learned int           `json:"learned_from"`
	Sources []styleSource `json:"sources"`
}

// emailPayload is a send_email action: the email plus its style note.
type emailPayload struct {
	provider.OutgoingEmail
	Style *styleNote `json:"style,omitempty"`
}

var greetingRe = regexp.MustCompile(`(?i)^(hi|hello|hey|dear|good (morning|afternoon|evening)|thanks|thank you)\b[^\n]{0,30}$`)

// greetingOf turns "Hi Sara," into "Hi {name},".
func greetingOf(first string) string {
	first = strings.TrimSpace(first)
	if !greetingRe.MatchString(first) {
		return ""
	}
	words := strings.Fields(first)
	if len(words) == 2 && !strings.EqualFold(words[1], "all,") && !strings.EqualFold(words[1], "team,") && !strings.EqualFold(words[1], "both,") && !strings.EqualFold(words[1], "there,") {
		lead := strings.ToLower(words[0])
		if lead == "hi" || lead == "hello" || lead == "hey" || lead == "dear" {
			punct := ""
			if strings.HasSuffix(words[1], ",") {
				punct = ","
			}
			return words[0] + " {name}" + punct
		}
	}
	return first
}

// signOffOf finds a closing like "Thanks,\nAlex" in the last lines.
func signOffOf(lines []string) string {
	n := len(lines)
	if n >= 2 {
		prev, last := strings.TrimSpace(lines[n-2]), strings.TrimSpace(lines[n-1])
		if strings.HasSuffix(prev, ",") && len(strings.Fields(prev)) <= 4 && len(strings.Fields(last)) <= 3 {
			return prev + " " + last
		}
	}
	if n >= 1 {
		if last := strings.TrimSpace(lines[n-1]); len(strings.Fields(last)) <= 4 {
			return last
		}
	}
	return ""
}

func topN(counts map[string]int, n int) []string {
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if counts[keys[i]] != counts[keys[j]] {
			return counts[keys[i]] > counts[keys[j]]
		}
		return keys[i] < keys[j]
	})
	if len(keys) > n {
		keys = keys[:n]
	}
	return keys
}

// learnStyle reads up to 20 recent sent emails.
func (p *Plugin) learnStyle(ctx context.Context, now time.Time) (writingStyle, error) {
	sent, err := p.mail.ListMessages(ctx, "in:sent newer_than:90d", 20)
	if err != nil {
		return writingStyle{}, err
	}
	st := writingStyle{LearnedAt: now, Greetings: []string{}, SignOffs: []string{}, Samples: []string{}, Sources: []styleSource{}}
	greet, sign := map[string]int{}, map[string]int{}
	words := 0
	for _, m := range sent {
		full, err := p.mail.GetMessage(ctx, m.ID)
		if err != nil {
			continue
		}
		body := plugin.StripQuoted(strings.ReplaceAll(full.Body, "\r\n", "\n"))
		var lines []string
		for _, l := range strings.Split(body, "\n") {
			if strings.TrimSpace(l) != "" {
				lines = append(lines, l)
			}
		}
		w := len(strings.Fields(body))
		if w < 5 || len(lines) == 0 {
			continue
		}
		words += w
		if g := greetingOf(lines[0]); g != "" {
			greet[g]++
		}
		if s := signOffOf(lines); s != "" {
			sign[s]++
		}
		if len(st.Samples) < 5 {
			ex := []rune(strings.Join(lines, "\n"))
			if len(ex) > 400 {
				ex = append(ex[:400], '…')
			}
			st.Samples = append(st.Samples, string(ex))
		}
		st.Sources = append(st.Sources, styleSource{Subject: m.Subject, To: m.To, SentAt: m.Date.Format(time.RFC3339)})
	}
	if n := len(st.Sources); n > 0 {
		st.AvgWords = words / n
	}
	st.Greetings, st.SignOffs = topN(greet, 3), topN(sign, 3)
	return st, nil
}

// style returns the saved style, learning it again when older than a week.
func (p *Plugin) style(ctx context.Context, refresh bool) (writingStyle, error) {
	var st writingStyle
	ok, _ := plugin.LoadSetting(ctx, p.settings, styleKey, &st)
	if ok && !refresh && time.Since(st.LearnedAt) < 7*24*time.Hour {
		return st, nil
	}
	st, err := p.learnStyle(ctx, time.Now())
	if err != nil {
		return st, err
	}
	if p.settings != nil && len(st.Sources) > 0 {
		_ = p.settings.Save(ctx, styleKey, st)
	}
	return st, nil
}

// noteFor describes the saved style for an email preview (nil if none).
func (p *Plugin) noteFor(ctx context.Context) *styleNote {
	var st writingStyle
	if ok, _ := plugin.LoadSetting(ctx, p.settings, styleKey, &st); !ok || len(st.Sources) == 0 {
		return nil
	}
	src := st.Sources
	if len(src) > 5 {
		src = src[:5]
	}
	return &styleNote{Learned: len(st.Sources), Sources: src}
}

func (p *Plugin) styleTool() plugin.Tool {
	return plugin.Tool{
		Name: "get_writing_style",
		Description: "Learn how the user writes email from their own sent mail: usual greetings, sign-offs, typical length and real excerpts. " +
			"Call it once per conversation before writing any email or reply, then match that voice. Lists the emails it learned from.",
		Properties: map[string]any{"refresh": map[string]any{"type": "boolean", "description": "Re-learn from the latest sent mail instead of the saved profile"}},
		Run: func(ctx context.Context, raw json.RawMessage) (any, error) {
			in, err := plugin.Decode[struct {
				Refresh bool `json:"refresh"`
			}](raw)
			if err != nil {
				return nil, err
			}
			st, err := p.style(ctx, in.Refresh)
			if err != nil {
				return nil, err
			}
			if len(st.Sources) == 0 {
				return map[string]string{"note": "No sent mail to learn from yet; write in a warm, concise first-person voice."}, nil
			}
			return map[string]any{
				"style": st,
				"how_to_use": "Match the greeting, sign-off, length and tone. Use the samples for voice only; never copy facts, names or promises from them. " +
					"When you queue or draft the email, tell the user it was written in their style, learned from " + itoa(len(st.Sources)) + " of their sent emails.",
			}, nil
		},
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
