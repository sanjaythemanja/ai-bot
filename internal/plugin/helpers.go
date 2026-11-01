package plugin

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Schema helpers for tool inputs.

func Str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func Num(desc string) map[string]any { return map[string]any{"type": "integer", "description": desc} }
func Strs(desc string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
}

// Props merges property maps.
func Props(ps ...map[string]any) map[string]any {
	out := map[string]any{}
	for _, p := range ps {
		for k, v := range p {
			out[k] = v
		}
	}
	return out
}

// Decode parses a tool input into T.
func Decode[T any](raw json.RawMessage) (T, error) {
	var v T
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, fmt.Errorf("invalid tool input: %w", err)
	}
	return v, nil
}

// Clock parses and formats times in the user's time zone.
type Clock struct{ Loc *time.Location }

// Parse accepts RFC 3339 and a few common local layouts.
func (c Clock) Parse(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"} {
		if v, err := time.ParseInLocation(layout, s, c.Loc); err == nil {
			return v, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse time %q; use RFC 3339 like 2026-01-02T15:04:05-07:00", s)
}

// Short formats a time like "Mon Jan 2 15:04" in the user's zone.
func (c Clock) Short(v time.Time) string { return v.In(c.Loc).Format("Mon Jan 2 15:04") }

// String-list helpers (case-insensitive, trimmed).

func AddUnique(list, add []string) []string {
	for _, a := range add {
		if a = strings.TrimSpace(a); a != "" && !ContainsFold(list, a) {
			list = append(list, a)
		}
	}
	return list
}

func Without(list, remove []string) []string {
	if len(remove) == 0 {
		return list
	}
	out := list[:0:0]
	for _, a := range list {
		if !ContainsFold(remove, a) {
			out = append(out, a)
		}
	}
	return out
}

func ContainsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(strings.TrimSpace(x), strings.TrimSpace(s)) {
			return true
		}
	}
	return false
}

// Norm lowercases and collapses whitespace (and literal "\n") for comparisons.
func Norm(s string) string {
	s = strings.ReplaceAll(strings.ToLower(s), `\n`, " ")
	return strings.Join(strings.Fields(s), " ")
}
