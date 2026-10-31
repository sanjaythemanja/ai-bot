package google

import (
	"context"
	"encoding/base64"
	"fmt"
	"html"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"

	"github.com/sanjayrj/ai-bot/internal/provider"
)

// Gmail implements provider.Mail.
type Gmail struct{ auth *Auth }

// NewGmail returns a Gmail provider backed by auth.
func NewGmail(auth *Auth) *Gmail { return &Gmail{auth: auth} }

var _ provider.Mail = (*Gmail)(nil)

func (g *Gmail) Connected() bool { return g.auth.Connected() }

func (g *Gmail) svc(ctx context.Context) (*gmail.Service, error) {
	c, err := g.auth.HTTPClient(ctx)
	if err != nil {
		return nil, err
	}
	return gmail.NewService(ctx, option.WithHTTPClient(c))
}

func (g *Gmail) ListMessages(ctx context.Context, query string, max int) ([]provider.Email, error) {
	s, err := g.svc(ctx)
	if err != nil {
		return nil, err
	}
	if max <= 0 || max > 50 {
		max = 15
	}
	res, err := s.Users.Messages.List("me").Q(query).MaxResults(int64(max)).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("gmail list: %w", err)
	}
	out := make([]provider.Email, 0, len(res.Messages))
	for _, m := range res.Messages {
		full, err := s.Users.Messages.Get("me", m.Id).Format("metadata").
			MetadataHeaders("From", "To", "Subject", "Date").Context(ctx).Do()
		if err != nil {
			return nil, fmt.Errorf("gmail get %s: %w", m.Id, err)
		}
		out = append(out, toEmail(full, false))
	}
	return out, nil
}

func (g *Gmail) GetMessage(ctx context.Context, id string) (provider.Email, error) {
	s, err := g.svc(ctx)
	if err != nil {
		return provider.Email{}, err
	}
	m, err := s.Users.Messages.Get("me", id).Format("full").Context(ctx).Do()
	if err != nil {
		return provider.Email{}, fmt.Errorf("gmail get %s: %w", id, err)
	}
	return toEmail(m, true), nil
}

func (g *Gmail) CreateDraft(ctx context.Context, msg provider.OutgoingEmail) (string, error) {
	s, err := g.svc(ctx)
	if err != nil {
		return "", err
	}
	raw, threadID, err := g.buildRaw(ctx, s, msg)
	if err != nil {
		return "", err
	}
	d, err := s.Users.Drafts.Create("me", &gmail.Draft{
		Message: &gmail.Message{Raw: raw, ThreadId: threadID},
	}).Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("gmail draft: %w", err)
	}
	return d.Id, nil
}

func (g *Gmail) Send(ctx context.Context, msg provider.OutgoingEmail) (string, error) {
	s, err := g.svc(ctx)
	if err != nil {
		return "", err
	}
	raw, threadID, err := g.buildRaw(ctx, s, msg)
	if err != nil {
		return "", err
	}
	m, err := s.Users.Messages.Send("me", &gmail.Message{Raw: raw, ThreadId: threadID}).Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("gmail send: %w", err)
	}
	return m.Id, nil
}

func (g *Gmail) ListDrafts(ctx context.Context, max int) ([]provider.Draft, error) {
	s, err := g.svc(ctx)
	if err != nil {
		return nil, err
	}
	if max <= 0 || max > 50 {
		max = 20
	}
	res, err := s.Users.Drafts.List("me").MaxResults(int64(max)).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("gmail drafts list: %w", err)
	}
	out := make([]provider.Draft, 0, len(res.Drafts))
	for _, d := range res.Drafts {
		full, err := g.GetDraft(ctx, d.Id)
		if err != nil {
			return nil, err
		}
		full.Message.Body = "" // keep listings small; use GetDraft for the body
		out = append(out, full)
	}
	return out, nil
}

func (g *Gmail) GetDraft(ctx context.Context, id string) (provider.Draft, error) {
	s, err := g.svc(ctx)
	if err != nil {
		return provider.Draft{}, err
	}
	d, err := s.Users.Drafts.Get("me", id).Format("full").Context(ctx).Do()
	if err != nil {
		return provider.Draft{}, fmt.Errorf("gmail draft get %s: %w", id, err)
	}
	m := d.Message
	msg := provider.OutgoingEmail{
		To:        splitAddrs(header(m, "To")),
		Cc:        splitAddrs(header(m, "Cc")),
		Bcc:       splitAddrs(header(m, "Bcc")),
		Subject:   header(m, "Subject"),
		ThreadID:  m.ThreadId,
		InReplyTo: header(m, "In-Reply-To"),
	}
	if m.Payload != nil {
		msg.Body = findPart(m.Payload, "text/plain")
		if msg.Body == "" {
			msg.Body = stripHTML(findPart(m.Payload, "text/html"))
		}
	}
	return provider.Draft{ID: d.Id, Message: msg, Snippet: html.UnescapeString(m.Snippet)}, nil
}

func (g *Gmail) UpdateDraft(ctx context.Context, id string, msg provider.OutgoingEmail) error {
	s, err := g.svc(ctx)
	if err != nil {
		return err
	}
	raw, threadID, err := g.buildRaw(ctx, s, msg)
	if err != nil {
		return err
	}
	_, err = s.Users.Drafts.Update("me", id, &gmail.Draft{Id: id, Message: &gmail.Message{Raw: raw, ThreadId: threadID}}).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("gmail draft update: %w", err)
	}
	return nil
}

func (g *Gmail) SendDraft(ctx context.Context, id string) (string, error) {
	s, err := g.svc(ctx)
	if err != nil {
		return "", err
	}
	m, err := s.Users.Drafts.Send("me", &gmail.Draft{Id: id}).Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("gmail draft send: %w", err)
	}
	return m.Id, nil
}

func (g *Gmail) DeleteDraft(ctx context.Context, id string) error {
	s, err := g.svc(ctx)
	if err != nil {
		return err
	}
	if err := s.Users.Drafts.Delete("me", id).Context(ctx).Do(); err != nil {
		return fmt.Errorf("gmail draft delete: %w", err)
	}
	return nil
}

func splitAddrs(h string) []string {
	if strings.TrimSpace(h) == "" {
		return nil
	}
	if list, err := mail.ParseAddressList(h); err == nil {
		out := make([]string, 0, len(list))
		for _, a := range list {
			out = append(out, a.Address)
		}
		return out
	}
	var out []string
	for _, p := range strings.Split(h, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// buildRaw renders an RFC 2822 message, threading it when ReplyToID is set.
func (g *Gmail) buildRaw(ctx context.Context, s *gmail.Service, msg provider.OutgoingEmail) (string, string, error) {
	var b strings.Builder
	threadID := msg.ThreadID
	subject := msg.Subject
	if msg.ReplyToID == "" && msg.InReplyTo != "" {
		fmt.Fprintf(&b, "In-Reply-To: %s\r\nReferences: %s\r\n", msg.InReplyTo, msg.InReplyTo)
	}
	if msg.ReplyToID != "" {
		orig, err := s.Users.Messages.Get("me", msg.ReplyToID).Format("metadata").
			MetadataHeaders("Message-ID", "Subject").Context(ctx).Do()
		if err != nil {
			return "", "", fmt.Errorf("gmail reply lookup: %w", err)
		}
		threadID = orig.ThreadId
		if mid := header(orig, "Message-ID"); mid != "" {
			fmt.Fprintf(&b, "In-Reply-To: %s\r\nReferences: %s\r\n", mid, mid)
		}
		if subject == "" {
			subject = header(orig, "Subject")
		}
		if !strings.HasPrefix(strings.ToLower(subject), "re:") {
			subject = "Re: " + subject
		}
	}
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(msg.To, ", "))
	if len(msg.Cc) > 0 {
		fmt.Fprintf(&b, "Cc: %s\r\n", strings.Join(msg.Cc, ", "))
	}
	if len(msg.Bcc) > 0 {
		fmt.Fprintf(&b, "Bcc: %s\r\n", strings.Join(msg.Bcc, ", "))
	}
	fmt.Fprintf(&b, "Subject: %s\r\n", mimeHeader(subject))
	b.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=\"UTF-8\"\r\n\r\n")
	b.WriteString(msg.Body)
	return base64.URLEncoding.EncodeToString([]byte(b.String())), threadID, nil
}

func mimeHeader(s string) string {
	for _, r := range s {
		if r > 127 {
			return "=?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte(s)) + "?="
		}
	}
	return s
}

func header(m *gmail.Message, name string) string {
	if m.Payload == nil {
		return ""
	}
	for _, h := range m.Payload.Headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}

func toEmail(m *gmail.Message, withBody bool) provider.Email {
	e := provider.Email{
		ID:       m.Id,
		ThreadID: m.ThreadId,
		From:     header(m, "From"),
		To:       header(m, "To"),
		Subject:  header(m, "Subject"),
		Snippet:  html.UnescapeString(m.Snippet),
		Date:     time.UnixMilli(m.InternalDate),
	}
	if d, err := mail.ParseDate(header(m, "Date")); err == nil {
		e.Date = d
	}
	for _, l := range m.LabelIds {
		if l == "UNREAD" {
			e.Unread = true
		}
	}
	if withBody && m.Payload != nil {
		body := findPart(m.Payload, "text/plain")
		if body == "" {
			body = stripHTML(findPart(m.Payload, "text/html"))
		}
		const maxBody = 20000
		if len(body) > maxBody {
			body = body[:maxBody] + "\n…[truncated]"
		}
		e.Body = body
	}
	return e
}

func findPart(p *gmail.MessagePart, mime string) string {
	if p.MimeType == mime && p.Body != nil && p.Body.Data != "" {
		if b, err := base64.URLEncoding.DecodeString(p.Body.Data); err == nil {
			return string(b)
		}
		if b, err := base64.RawURLEncoding.DecodeString(p.Body.Data); err == nil {
			return string(b)
		}
	}
	for _, c := range p.Parts {
		if s := findPart(c, mime); s != "" {
			return s
		}
	}
	return ""
}

var (
	tagRe   = regexp.MustCompile(`(?s)<(script|style).*?</(script|style)>|<[^>]+>`)
	spaceRe = regexp.MustCompile(`\n\s*\n+`)
)

func stripHTML(s string) string {
	s = tagRe.ReplaceAllString(s, "\n")
	return strings.TrimSpace(spaceRe.ReplaceAllString(html.UnescapeString(s), "\n\n"))
}
