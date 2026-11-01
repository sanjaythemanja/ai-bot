// Package gmail is the Gmail plugin: search and read mail, manage drafts,
// and send email (as approvable actions).
package gmail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/sanjayrj/ai-bot/internal/plugin"
	"github.com/sanjayrj/ai-bot/internal/provider"
)

// Action kinds this plugin executes.
const (
	KindSendEmail   = "send_email"
	KindSendDraft   = "send_draft"
	KindDeleteDraft = "delete_draft"
)

// Plugin implements plugin.Plugin, Executor, Editor, Deduper, Guide and
// Sorter.
type Plugin struct {
	mail     provider.Mail
	settings plugin.Settings // sorting rules and the learned writing style; may be nil
}

func New(mail provider.Mail, settings plugin.Settings) *Plugin {
	return &Plugin{mail: mail, settings: settings}
}

var (
	_ plugin.Executor = (*Plugin)(nil)
	_ plugin.Editor   = (*Plugin)(nil)
	_ plugin.Deduper  = (*Plugin)(nil)
	_ plugin.Guide    = (*Plugin)(nil)
	_ plugin.Sorter   = (*Plugin)(nil)
)

func (p *Plugin) Name() string { return "gmail" }

func (p *Plugin) Guidance() string {
	return `- Email: use draft_email when the user wants to review wording; update_draft edits a draft (to/cc/bcc/subject/body). Never invent facts, commitments or numbers.
- Voice: before writing any email or reply, call get_writing_style (once per conversation) and write like the user: their greeting, sign-off, length and tone.
- When you email someone about a meeting, include its title, date, time (with time zone) and the event link.
- Priorities: use triage_inbox to decide what matters and always say why (its reasons). When the user corrects a priority ("Acme is always important", "ignore LinkedIn"), save it with add_sorting_rule.
- Follow-ups: find_awaiting_replies lists mail nobody answered; find_my_commitments lists promises the user made. For a nudge, reply in the same thread (reply_to_id) briefly and politely.`
}

// changes is a partial edit to an email. List fields replace the whole list;
// add_* append; remove drops an address from to, cc and bcc.
type changes struct {
	To      *[]string `json:"to"`
	Cc      *[]string `json:"cc"`
	Bcc     *[]string `json:"bcc"`
	AddTo   []string  `json:"add_to"`
	AddCc   []string  `json:"add_cc"`
	AddBcc  []string  `json:"add_bcc"`
	Remove  []string  `json:"remove"`
	Subject *string   `json:"subject"`
	Body    *string   `json:"body"`
}

var changeProps = map[string]any{
	"to":      plugin.Strs("Replace the To list"),
	"cc":      plugin.Strs("Replace the CC list"),
	"bcc":     plugin.Strs("Replace the BCC list"),
	"add_to":  plugin.Strs("Addresses to add to To"),
	"add_cc":  plugin.Strs("Addresses to add to CC"),
	"add_bcc": plugin.Strs("Addresses to add to BCC"),
	"remove":  plugin.Strs("Addresses to remove from To, CC and BCC"),
	"subject": plugin.Str("New subject"),
	"body":    plugin.Str("New plain-text body (replaces the old one)"),
}

func (c changes) apply(m provider.OutgoingEmail) provider.OutgoingEmail {
	if c.To != nil {
		m.To = *c.To
	}
	if c.Cc != nil {
		m.Cc = *c.Cc
	}
	if c.Bcc != nil {
		m.Bcc = *c.Bcc
	}
	m.To = plugin.AddUnique(m.To, c.AddTo)
	m.Cc = plugin.AddUnique(m.Cc, c.AddCc)
	m.Bcc = plugin.AddUnique(m.Bcc, c.AddBcc)
	m.To, m.Cc, m.Bcc = plugin.Without(m.To, c.Remove), plugin.Without(m.Cc, c.Remove), plugin.Without(m.Bcc, c.Remove)
	if c.Subject != nil {
		m.Subject = *c.Subject
	}
	if c.Body != nil {
		m.Body = *c.Body
	}
	return m
}

func summary(prefix string, m provider.OutgoingEmail) string {
	subj := m.Subject
	if subj == "" {
		subj = "(reply)"
	}
	s := fmt.Sprintf("%s %s — %q", prefix, strings.Join(m.To, ", "), subj)
	if len(m.Cc) > 0 {
		s += " cc " + strings.Join(m.Cc, ", ")
	}
	if len(m.Bcc) > 0 {
		s += " bcc " + strings.Join(m.Bcc, ", ")
	}
	return s
}

// draftRef identifies a draft in a send/delete action; Message is a snapshot
// for the approval preview.
type draftRef struct {
	DraftID string                  `json:"draft_id"`
	Summary string                  `json:"summary"`
	Message *provider.OutgoingEmail `json:"message,omitempty"`
}

func (p *Plugin) Tools() []plugin.Tool {
	emailProps := map[string]any{
		"to":          plugin.Strs("Recipient email addresses"),
		"cc":          plugin.Strs("CC addresses"),
		"bcc":         plugin.Strs("BCC addresses (hidden from other recipients)"),
		"subject":     plugin.Str("Subject line (optional when replying)"),
		"body":        plugin.Str("Plain-text body, written in the user's voice"),
		"reply_to_id": plugin.Str("Message ID being replied to, to keep the thread"),
	}
	draftID := map[string]any{"draft_id": plugin.Str("Draft ID")}

	tools := []plugin.Tool{
		{
			Name:        "search_emails",
			Description: "Search the user's Gmail. Supports Gmail query syntax (e.g. 'is:unread in:inbox newer_than:2d', 'from:priya'). Returns headers and snippets, not bodies.",
			Properties:  map[string]any{"query": plugin.Str("Gmail search query"), "max_results": plugin.Num("Max messages, default 10, max 50")},
			Required:    []string{"query"},
			Run: func(ctx context.Context, raw json.RawMessage) (any, error) {
				in, err := plugin.Decode[struct {
					Query string `json:"query"`
					Max   int    `json:"max_results"`
				}](raw)
				if err != nil {
					return nil, err
				}
				if in.Max == 0 {
					in.Max = 10
				}
				return p.mail.ListMessages(ctx, in.Query, in.Max)
			},
		},
		{
			Name:        "read_email",
			Description: "Read the full body of one email by its message ID.",
			Properties:  map[string]any{"message_id": plugin.Str("Message ID from search_emails")},
			Required:    []string{"message_id"},
			Run: func(ctx context.Context, raw json.RawMessage) (any, error) {
				in, err := plugin.Decode[struct {
					ID string `json:"message_id"`
				}](raw)
				if err != nil {
					return nil, err
				}
				return p.mail.GetMessage(ctx, in.ID)
			},
		},
		{
			Name:        "draft_email",
			Mutates:     true,
			Description: "Save an email as a Gmail draft. Safe: nothing is sent. Prefer this when the user wants to review wording themselves.",
			Properties:  emailProps,
			Required:    []string{"to", "body"},
			Run: func(ctx context.Context, raw json.RawMessage) (any, error) {
				in, err := plugin.Decode[provider.OutgoingEmail](raw)
				if err != nil {
					return nil, err
				}
				id, err := p.mail.CreateDraft(ctx, in)
				if err != nil {
					return nil, err
				}
				out := map[string]any{"status": "draft_saved", "draft_id": id}
				if n := p.noteFor(ctx); n != nil {
					out["written_in_style_learned_from"] = n.Learned
				}
				return out, nil
			},
		},
		{
			Name:        "send_email",
			Description: "Send an email.",
			Properties:  emailProps,
			Required:    []string{"to", "body"},
			Write:       true,
			Propose: func(ctx context.Context, raw json.RawMessage) (plugin.Proposal, error) {
				in, err := plugin.Decode[provider.OutgoingEmail](raw)
				if err != nil {
					return plugin.Proposal{}, err
				}
				if len(in.To) == 0 {
					return plugin.Proposal{}, errors.New("at least one recipient is required")
				}
				pl := emailPayload{OutgoingEmail: in, Style: p.noteFor(ctx)}
				return plugin.Proposal{Kind: KindSendEmail, Summary: summary("Email", in), Payload: pl}, nil
			},
		},
		{
			Name:        "list_drafts",
			Description: "List the user's Gmail drafts (recipients and subject, no body).",
			Properties:  map[string]any{"max_results": plugin.Num("Max drafts, default 20")},
			Run: func(ctx context.Context, raw json.RawMessage) (any, error) {
				in, _ := plugin.Decode[struct {
					Max int `json:"max_results"`
				}](raw)
				return p.mail.ListDrafts(ctx, in.Max)
			},
		},
		{
			Name:        "read_draft",
			Description: "Read one Gmail draft in full, including its body.",
			Properties:  draftID,
			Required:    []string{"draft_id"},
			Run: func(ctx context.Context, raw json.RawMessage) (any, error) {
				in, err := plugin.Decode[draftRef](raw)
				if err != nil {
					return nil, err
				}
				return p.mail.GetDraft(ctx, in.DraftID)
			},
		},
		{
			Name:        "update_draft",
			Mutates:     true,
			Description: "Edit a Gmail draft: recipients, CC, BCC, subject or body. Only the fields you pass change. Safe: nothing is sent.",
			Properties:  plugin.Props(draftID, changeProps),
			Required:    []string{"draft_id"},
			Run: func(ctx context.Context, raw json.RawMessage) (any, error) {
				in, err := plugin.Decode[struct {
					DraftID string `json:"draft_id"`
					changes
				}](raw)
				if err != nil {
					return nil, err
				}
				d, err := p.mail.GetDraft(ctx, in.DraftID)
				if err != nil {
					return nil, err
				}
				msg := in.changes.apply(d.Message)
				if err := p.mail.UpdateDraft(ctx, in.DraftID, msg); err != nil {
					return nil, err
				}
				return map[string]any{"status": "draft_updated", "draft_id": in.DraftID, "draft": msg}, nil
			},
		},
		{
			Name:        "send_draft",
			Description: "Send an existing Gmail draft as it is.",
			Properties:  draftID,
			Required:    []string{"draft_id"},
			Write:       true,
			Propose: func(ctx context.Context, raw json.RawMessage) (plugin.Proposal, error) {
				return p.draftProposal(ctx, raw, KindSendDraft, "Send draft")
			},
		},
		{
			Name:        "delete_draft",
			Description: "Delete a Gmail draft.",
			Properties:  draftID,
			Required:    []string{"draft_id"},
			Write:       true,
			Propose: func(ctx context.Context, raw json.RawMessage) (plugin.Proposal, error) {
				return p.draftProposal(ctx, raw, KindDeleteDraft, "Delete draft")
			},
		},
		p.styleTool(),
	}
	tools = append(tools, p.followUpTools()...)
	return append(tools, p.sortingTools()...)
}

func (p *Plugin) draftProposal(ctx context.Context, raw json.RawMessage, kind, prefix string) (plugin.Proposal, error) {
	in, err := plugin.Decode[draftRef](raw)
	if err != nil {
		return plugin.Proposal{}, err
	}
	d, err := p.mail.GetDraft(ctx, in.DraftID)
	if err != nil {
		return plugin.Proposal{}, err
	}
	if kind == KindSendDraft && len(d.Message.To) == 0 {
		return plugin.Proposal{}, errors.New("the draft has no recipients; add some with update_draft first")
	}
	ref := draftRef{DraftID: in.DraftID, Summary: summary(prefix, d.Message), Message: &d.Message}
	return plugin.Proposal{Kind: kind, Summary: ref.Summary, Payload: ref}, nil
}

// ---- Executor ----

func (p *Plugin) Kinds() []string { return []string{KindSendEmail, KindSendDraft, KindDeleteDraft} }

func (p *Plugin) Execute(ctx context.Context, kind string, payload json.RawMessage) (string, error) {
	switch kind {
	case KindSendEmail:
		var m provider.OutgoingEmail
		if err := json.Unmarshal(payload, &m); err != nil {
			return "", err
		}
		id, err := p.mail.Send(ctx, m)
		if err != nil {
			return "", err
		}
		return "Email sent (id " + id + ")", nil
	case KindSendDraft, KindDeleteDraft:
		var r draftRef
		if err := json.Unmarshal(payload, &r); err != nil {
			return "", err
		}
		if kind == KindDeleteDraft {
			if err := p.mail.DeleteDraft(ctx, r.DraftID); err != nil {
				return "", err
			}
			return "Draft deleted", nil
		}
		id, err := p.mail.SendDraft(ctx, r.DraftID)
		if err != nil {
			return "", err
		}
		return "Draft sent (id " + id + ")", nil
	}
	return "", fmt.Errorf("gmail: unknown action kind %q", kind)
}

// ---- Editor ----

func (p *Plugin) EditFields(kind string) map[string]any {
	if kind == KindSendEmail {
		return changeProps
	}
	return nil
}

func (p *Plugin) Edit(_ context.Context, kind string, payload, raw json.RawMessage) (plugin.Proposal, error) {
	var pl emailPayload
	if err := json.Unmarshal(payload, &pl); err != nil {
		return plugin.Proposal{}, err
	}
	ch, err := plugin.Decode[changes](raw)
	if err != nil {
		return plugin.Proposal{}, err
	}
	pl.OutgoingEmail = ch.apply(pl.OutgoingEmail)
	if len(pl.To) == 0 {
		return plugin.Proposal{}, errors.New("the email would have no recipients")
	}
	return plugin.Proposal{Kind: kind, Summary: summary("Email", pl.OutgoingEmail), Payload: pl}, nil
}

// ---- Deduper ----

// Duplicate: same subject and body (e.g. only the recipient was corrected),
// or same recipients and subject (only the wording changed); for drafts,
// the same draft.
func (p *Plugin) Duplicate(kind string, pending, proposed json.RawMessage) bool {
	if kind == KindSendDraft || kind == KindDeleteDraft {
		var a, b draftRef
		_ = json.Unmarshal(pending, &a)
		_ = json.Unmarshal(proposed, &b)
		return a.DraftID != "" && a.DraftID == b.DraftID
	}
	var a, b provider.OutgoingEmail
	if json.Unmarshal(pending, &a) != nil || json.Unmarshal(proposed, &b) != nil {
		return false
	}
	sameSubject := plugin.Norm(a.Subject) == plugin.Norm(b.Subject)
	return (sameSubject && plugin.Norm(a.Body) == plugin.Norm(b.Body)) ||
		(sameSubject && addrSet(a.To) == addrSet(b.To))
}

func addrSet(list []string) string {
	out := make([]string, 0, len(list))
	for _, x := range list {
		out = append(out, strings.ToLower(strings.TrimSpace(x)))
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}
