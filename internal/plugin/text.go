package plugin

import (
	"regexp"
	"strings"
)

// Text helpers shared by plugins that read what the user wrote.

var (
	quoteHeaderRe = regexp.MustCompile(`(?im)^(on .+wrote:|-{2,} ?original message ?-{2,}|from: .+@.+)\s*$`)
	sentenceEndRe = regexp.MustCompile(`[.!?]+\s+|\n+`)
	promiseRe     = regexp.MustCompile(`(?i)\b(i'll|i will|i'm going to|i am going to|i can (send|share|get)|let me (send|check|get back)|we'll|we will|will (send|share|get back|follow up|circle back|review|update|confirm))\b`)
)

// StripQuoted drops the quoted earlier messages from an email reply, keeping
// only what the sender wrote this time.
func StripQuoted(body string) string {
	if loc := quoteHeaderRe.FindStringIndex(body); loc != nil {
		body = body[:loc[0]]
	}
	var keep []string
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), ">") {
			keep = append(keep, line)
		}
	}
	return strings.TrimSpace(strings.Join(keep, "\n"))
}

// Promises returns the sentences in text where the writer commits to doing
// something ("I'll send the deck by Friday"). At most max are returned.
func Promises(text string, max int) []string {
	text = strings.NewReplacer("’", "'", "‘", "'").Replace(text)
	var out []string
	for _, s := range sentenceEndRe.Split(text, -1) {
		s = strings.TrimSpace(s)
		if s == "" || !promiseRe.MatchString(s) {
			continue
		}
		if r := []rune(s); len(r) > 220 {
			s = string(r[:220]) + "…"
		}
		out = append(out, s)
		if max > 0 && len(out) >= max {
			break
		}
	}
	return out
}

// Addr extracts the bare, lowercased address from "Name <a@b.com>".
func Addr(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "<"); i >= 0 {
		if j := strings.Index(s[i:], ">"); j > 0 {
			s = s[i+1 : i+j]
		}
	}
	return strings.ToLower(strings.Trim(s, `" `))
}

// Addrs splits a header like "A <a@x.com>, b@y.com" into bare addresses.
func Addrs(header string) []string {
	var out []string
	for _, p := range strings.Split(header, ",") {
		if a := Addr(p); strings.Contains(a, "@") {
			out = append(out, a)
		}
	}
	return out
}

// escapedBreaks turns escape sequences a model wrote out literally ("\n" as
// a backslash and an n) back into real line breaks and tabs.
var escapedBreaks = strings.NewReplacer(`\r\n`, "\n", `\n`, "\n", `\t`, "\t")

// CleanText prepares model-written text for sending: real line breaks
// instead of literal "\n", and no stray whitespace at either end.
func CleanText(s string) string {
	return strings.TrimSpace(escapedBreaks.Replace(s))
}

var placeholderRe = regexp.MustCompile(`(?i)\[\s*(your|my|insert|recipient|sender|first name|full name|name|company|date|time)\b[^\]\n]{0,30}\]`)

// Placeholder returns the first fill-in-the-blank left in text, such as
// "[Your Name]", or "".
func Placeholder(text string) string {
	return placeholderRe.FindString(text)
}
