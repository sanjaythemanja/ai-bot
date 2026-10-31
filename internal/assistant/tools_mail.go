package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/sanjayrj/ai-bot/internal/provider"
)

// emailChanges is a partial edit to an email. List fields replace the whole
// list; add_* append; remove drops an address from to, cc and bcc.
type emailChanges struct {
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

var emailChangeProps = prop{
	"to":      strs("Replace the To list"),
	"cc":      strs("Replace the CC list"),
	"bcc":     strs("Replace the BCC list"),
	"add_to":  strs("Addresses to add to To"),
	"add_cc":  strs("Addresses to add to CC"),
	"add_bcc": strs("Addresses to add to BCC"),
	"remove":  strs("Addresses to remove from To, CC and BCC"),
	"subject": str("New subject"),
	"body":    str("New plain-text body (replaces the old one)"),
}

func (c emailChanges) apply(m provider.OutgoingEmail) provider.OutgoingEmail {
	if c.To != nil {
		m.To = *c.To
	}
	if c.Cc != nil {
		m.Cc = *c.Cc
	}
	if c.Bcc != nil {
		m.Bcc = *c.Bcc
	}
	m.To = addUnique(m.To, c.AddTo)
	m.Cc = addUnique(m.Cc, c.AddCc)
	m.Bcc = addUnique(m.Bcc, c.AddBcc)
	m.To, m.Cc, m.Bcc = without(m.To, c.Remove), without(m.Cc, c.Remove), without(m.Bcc, c.Remove)
	if c.Subject != nil {
		m.Subject = *c.Subject
	}
	if c.Body != nil {
		m.Body = *c.Body
	}
	return m
}

func emailSummary(prefix string, m provider.OutgoingEmail) string {
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

// draftRef identifies a draft in a send/delete action.
type draftRef struct {
	DraftID string `json:"draft_id"`
	Summary string `json:"summary"`
	// Message is a snapshot of the draft when queued, for the approval preview.
	Message *provider.OutgoingEmail `json:"message,omitempty"`
}

func (t *Toolbox) registerMail() {
	t.add("search_emails",
		"Search the user's Gmail. Supports Gmail query syntax (e.g. 'is:unread in:inbox newer_than:2d', 'from:priya'). Returns headers and snippets, not bodies.",
		prop{"query": str("Gmail search query"), "max_results": num("Max messages, default 10, max 50")},
		[]string{"query"},
		func(ctx context.Context, raw json.RawMessage) (any, error) {
			in, err := decode[struct {
				Query string `json:"query"`
				Max   int    `json:"max_results"`
			}](raw)
			if err != nil {
				return nil, err
			}
			if in.Max == 0 {
				in.Max = 10
			}
			return t.mail.ListMessages(ctx, in.Query, in.Max)
		})

	t.add("read_email", "Read the full body of one email by its message ID.",
		prop{"message_id": str("Message ID from search_emails")}, []string{"message_id"},
		func(ctx context.Context, raw json.RawMessage) (any, error) {
			in, err := decode[struct {
				ID string `json:"message_id"`
			}](raw)
			if err != nil {
				return nil, err
			}
			return t.mail.GetMessage(ctx, in.ID)
		})

	emailProps := prop{
		"to":          strs("Recipient email addresses"),
		"cc":          strs("CC addresses"),
		"bcc":         strs("BCC addresses (hidden from other recipients)"),
		"subject":     str("Subject line (optional when replying)"),
		"body":        str("Plain-text body, written in the user's voice"),
		"reply_to_id": str("Message ID being replied to, to keep the thread"),
	}
	t.add("draft_email",
		"Save an email as a Gmail draft. Safe: nothing is sent. Prefer this when the user wants to review wording themselves.",
		emailProps, []string{"to", "body"},
		func(ctx context.Context, raw json.RawMessage) (any, error) {
			in, err := decode[provider.OutgoingEmail](raw)
			if err != nil {
				return nil, err
			}
			id, err := t.mail.CreateDraft(ctx, in)
			if err != nil {
				return nil, err
			}
			return map[string]string{"status": "draft_saved", "draft_id": id}, nil
		})

	t.add("send_email", t.writeDesc("Send an email."), emailProps, []string{"to", "body"},
		func(ctx context.Context, raw json.RawMessage) (any, error) {
			in, err := decode[provider.OutgoingEmail](raw)
			if err != nil {
				return nil, err
			}
			if len(in.To) == 0 {
				return nil, errors.New("at least one recipient is required")
			}
			return t.queue(ctx, ActionSendEmail, emailSummary("Email", in), in)
		})

	// ---- Drafts ----
	t.add("list_drafts", "List the user's Gmail drafts (recipients and subject, no body).",
		prop{"max_results": num("Max drafts, default 20")}, nil,
		func(ctx context.Context, raw json.RawMessage) (any, error) {
			in, _ := decode[struct {
				Max int `json:"max_results"`
			}](raw)
			return t.mail.ListDrafts(ctx, in.Max)
		})

	t.add("read_draft", "Read one Gmail draft in full, including its body.",
		prop{"draft_id": str("Draft ID")}, []string{"draft_id"},
		func(ctx context.Context, raw json.RawMessage) (any, error) {
			in, err := decode[draftRef](raw)
			if err != nil {
				return nil, err
			}
			return t.mail.GetDraft(ctx, in.DraftID)
		})

	t.add("update_draft",
		"Edit a Gmail draft: recipients, CC, BCC, subject or body. Only the fields you pass change. Safe: nothing is sent.",
		merge(prop{"draft_id": str("Draft ID")}, emailChangeProps), []string{"draft_id"},
		func(ctx context.Context, raw json.RawMessage) (any, error) {
			in, err := decode[struct {
				draftRef
				emailChanges
			}](raw)
			if err != nil {
				return nil, err
			}
			d, err := t.mail.GetDraft(ctx, in.DraftID)
			if err != nil {
				return nil, err
			}
			msg := in.emailChanges.apply(d.Message)
			if err := t.mail.UpdateDraft(ctx, in.DraftID, msg); err != nil {
				return nil, err
			}
			return map[string]any{"status": "draft_updated", "draft_id": in.DraftID, "draft": msg}, nil
		})

	t.add("send_draft", t.writeDesc("Send an existing Gmail draft as it is."),
		prop{"draft_id": str("Draft ID")}, []string{"draft_id"},
		func(ctx context.Context, raw json.RawMessage) (any, error) {
			in, err := decode[draftRef](raw)
			if err != nil {
				return nil, err
			}
			d, err := t.mail.GetDraft(ctx, in.DraftID)
			if err != nil {
				return nil, err
			}
			if len(d.Message.To) == 0 {
				return nil, errors.New("the draft has no recipients; add some with update_draft first")
			}
			ref := draftRef{DraftID: in.DraftID, Summary: emailSummary("Send draft", d.Message), Message: &d.Message}
			return t.queue(ctx, ActionSendDraft, ref.Summary, ref)
		})

	t.add("delete_draft", t.writeDesc("Delete a Gmail draft."),
		prop{"draft_id": str("Draft ID")}, []string{"draft_id"},
		func(ctx context.Context, raw json.RawMessage) (any, error) {
			in, err := decode[draftRef](raw)
			if err != nil {
				return nil, err
			}
			d, err := t.mail.GetDraft(ctx, in.DraftID)
			if err != nil {
				return nil, err
			}
			ref := draftRef{DraftID: in.DraftID, Summary: emailSummary("Delete draft", d.Message), Message: &d.Message}
			return t.queue(ctx, ActionDeleteDraft, ref.Summary, ref)
		})
}

func addUnique(list, add []string) []string {
	for _, a := range add {
		if a = strings.TrimSpace(a); a != "" && !containsFold(list, a) {
			list = append(list, a)
		}
	}
	return list
}

func without(list, remove []string) []string {
	if len(remove) == 0 {
		return list
	}
	out := list[:0:0]
	for _, a := range list {
		if !containsFold(remove, a) {
			out = append(out, a)
		}
	}
	return out
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(strings.TrimSpace(x), strings.TrimSpace(s)) {
			return true
		}
	}
	return false
}
