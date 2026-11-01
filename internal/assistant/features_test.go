package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sanjayrj/ai-bot/internal/auth"
	"github.com/sanjayrj/ai-bot/internal/provider"
	"github.com/sanjayrj/ai-bot/internal/store"
)

func todayDate() string    { return time.Now().UTC().Format("2006-01-02") }
func tomorrowDate() string { return time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02") }

func callList(t *testing.T, srvURL string, name, input string) []map[string]any {
	t.Helper()
	var out ToolOutput
	if code := postJSON(t, srvURL+"/api/tools/"+name, input, &out); code != 200 || out.Error != "" {
		t.Fatalf("%s: code %d error %q", name, code, out.Error)
	}
	var list []map[string]any
	if err := json.Unmarshal(out.Result, &list); err != nil {
		t.Fatalf("%s result is not a list: %s", name, out.Result)
	}
	return list
}

func TestFollowUpsAcrossMailAndSlack(t *testing.T) {
	srv := newTestServer(t, nil)

	waiting := callList(t, srv.URL, "find_awaiting_replies", `{}`)
	var subjects []string
	for _, w := range waiting {
		subjects = append(subjects, w["subject"].(string))
	}
	got := strings.Join(subjects, " | ")
	// Vendor (4 days) and finance (3 days) never replied; Sara did; the
	// Priya reply is only an hour old.
	if len(waiting) != 2 || !strings.Contains(got, "laptops") || !strings.Contains(got, "September") {
		t.Fatalf("awaiting replies = %s", got)
	}

	// min_days 0 also counts mail sent today (the Priya reply, an hour old).
	if today := callList(t, srv.URL, "find_awaiting_replies", `{"min_days":0}`); len(today) != 3 {
		t.Fatalf("min_days 0 should include today's mail, got %d", len(today))
	}

	promises := callList(t, srv.URL, "find_my_commitments", `{}`)
	all, _ := json.Marshal(promises)
	if !strings.Contains(string(all), "updated hiring plan by Friday") || !strings.Contains(string(all), "decision by Thursday") {
		t.Fatalf("commitments missing: %s", all)
	}
	if strings.Contains(string(all), "revised term sheet") {
		t.Fatalf("quoted text must not count as the user's promise: %s", all)
	}

	slack := callList(t, srv.URL, "find_slack_commitments", `{}`)
	if len(slack) != 1 || !strings.Contains(slack[0]["text"].(string), "hiring plan numbers by Wednesday") {
		t.Fatalf("slack commitments = %+v", slack)
	}
}

func TestSortingExplainsAndRulesStick(t *testing.T) {
	srv := newTestServer(t, nil)

	byFrom := func() map[string]map[string]any {
		out := map[string]map[string]any{}
		for _, e := range callList(t, srv.URL, "triage_inbox", `{"query":"in:inbox"}`) {
			out[e["from"].(string)] = e
		}
		return out
	}
	list := byFrom()
	priya := list["Priya Shah <priya@example.com>"]
	if priya["priority"] != "important" || !strings.Contains(strings.Join(toStrings(priya["reasons"]), " "), "deadline") {
		t.Fatalf("Priya's sign-off email should be important with a deadline reason: %+v", priya)
	}
	digest := list["Weekly Digest <digest@example.com>"]
	if digest["priority"] != "low" {
		t.Fatalf("digest should be low: %+v", digest)
	}

	call(t, srv, "add_sorting_rule", `{"match":"@example.com","priority":"low"}`)
	call(t, srv, "add_sorting_rule", `{"match":"Billing","priority":"important"}`)
	list = byFrom()
	billing := list["Cloud Billing <billing@example.com>"]
	if billing["priority"] != "important" || !strings.Contains(toStrings(billing["reasons"])[0], "Your rule") {
		t.Fatalf("billing rule should win: %+v", billing)
	}
	if list["Priya Shah <priya@example.com>"]["priority"] != "low" {
		t.Fatal("the @example.com rule should apply")
	}
	call(t, srv, "remove_sorting_rule", `{"match":"@example.com"}`)
	if byFrom()["Priya Shah <priya@example.com>"]["priority"] != "important" {
		t.Fatal("removing the rule should restore the normal ranking")
	}

	var ov Overview
	getJSON(t, srv.URL+"/api/overview", &ov)
	if len(ov.Priority) == 0 || ov.Priority["m1"].Priority != "important" {
		t.Fatalf("overview should carry explained priorities: %+v", ov.Priority)
	}
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

func TestProtectedTimeRules(t *testing.T) {
	srv := newTestServer(t, nil)
	call(t, srv, "set_time_rules", `{"work_start":"09:00","work_end":"18:00",
		"protected":[{"label":"Focus","days":["monday","tue","wed","thu","fri","sat","sun"],"start":"16:00","end":"17:30"}]}`)

	rules := call(t, srv, "get_time_rules", `{}`)
	if b, _ := json.Marshal(rules); !strings.Contains(string(b), `"days":["mon","tue"`) {
		t.Fatalf("days should be normalized: %s", b)
	}

	// Slot search skips the focus block.
	tomorrow := tomorrowDate()
	res := call(t, srv, "find_free_slots", `{"date":"`+tomorrow+`","duration_minutes":30}`)
	if b, _ := json.Marshal(res); !strings.Contains(string(b), "Focus 16:00–17:30") || !strings.Contains(string(b), `"start":"`+tomorrow+`T17:30:00Z"`) {
		t.Fatalf("expected focus block skipped: %s", b)
	}

	// Booking over it (and outside hours) is allowed but flagged.
	out := call(t, srv, "create_event", `{"title":"Late sync","start":"`+tomorrow+`T16:30:00Z","end":"`+tomorrow+`T18:30:00Z"}`)
	sum := out["summary"].(string)
	if !strings.Contains(sum, `protected time "Focus"`) || !strings.Contains(sum, "Outside your working hours") {
		t.Fatalf("summary should warn: %s", sum)
	}
	var acts struct{ Actions []Action }
	getJSON(t, srv.URL+"/api/actions", &acts)
	if !strings.Contains(string(acts.Actions[0].Payload), `"warnings"`) {
		t.Fatalf("payload should carry warnings for the preview: %s", acts.Actions[0].Payload)
	}

	// A clash with an existing meeting is flagged too.
	moved := call(t, srv, "update_event", `{"event_id":"e4","start":"`+todayDate()+`T09:45:00Z"}`)
	if !strings.Contains(moved["summary"].(string), `Clashes with "Exec staff sync"`) {
		t.Fatalf("expected clash warning: %s", moved["summary"])
	}

	if res := postJSON(t, srv.URL+"/api/tools/set_time_rules", `{"work_start":"25:00"}`, &ToolOutput{}); res != 200 {
		t.Fatalf("bad rule should be a tool error, not a transport error: %d", res)
	}
}

func TestWritingStyleLearnedFromSentMail(t *testing.T) {
	srv := newTestServer(t, nil)
	res := call(t, srv, "get_writing_style", `{}`)
	b, _ := json.Marshal(res)
	s := string(b)
	for _, want := range []string{`"Hi {name},"`, "Thanks, Alex", "Quote for 40 laptops"} {
		if !strings.Contains(s, want) {
			t.Fatalf("style missing %q: %s", want, s)
		}
	}
	// Queued emails now say which emails the voice was learned from.
	call(t, srv, "send_email", `{"to":["dana@example.com"],"subject":"Call","body":"Hi Dana,\n\nHappy to talk.\n\nThanks,\nAlex"}`)
	var acts struct{ Actions []Action }
	getJSON(t, srv.URL+"/api/actions", &acts)
	if !strings.Contains(string(acts.Actions[0].Payload), `"learned_from":4`) {
		t.Fatalf("email action should carry the style note: %s", acts.Actions[0].Payload)
	}
	// Editing keeps the note.
	call(t, srv, "edit_pending_action", `{"action_id":"`+acts.Actions[0].ID+`","subject":"Quick call"}`)
	getJSON(t, srv.URL+"/api/actions", &acts)
	if !strings.Contains(string(acts.Actions[0].Payload), `"learned_from":4`) || !strings.Contains(string(acts.Actions[0].Payload), "Quick call") {
		t.Fatalf("edit lost the style note: %s", acts.Actions[0].Payload)
	}
}

func TestActionsRecordWhy(t *testing.T) {
	srv := newTestServer(t, nil)
	call(t, srv, "send_email", `{"to":["dana@example.com"],"body":"hi"}`)
	var acts struct{ Actions []Action }
	getJSON(t, srv.URL+"/api/actions", &acts)
	if acts.Actions[0].Reason == "" {
		t.Fatal("actions from the tools API should still say why they exist")
	}
}

func TestEmailsGetRealLineBreaksAndNoPlaceholders(t *testing.T) {
	srv := newTestServer(t, nil)

	// A model that writes "\n" literally still produces real line breaks.
	d := call(t, srv, "draft_email", `{"to":["rakshit@example.com"],"subject":"Run","body":"Hey Rakshit,\\n\\nRun at 6.\\n\\nBest,\\nSanjay"}`)
	got := call(t, srv, "read_draft", `{"draft_id":"`+d["draft_id"].(string)+`"}`)
	body := got["message"].(map[string]any)["body"].(string)
	if body != "Hey Rakshit,\n\nRun at 6.\n\nBest,\nSanjay" {
		t.Fatalf("draft body not cleaned: %q", body)
	}

	// A placeholder is refused back to the model instead of being sent.
	var out ToolOutput
	postJSON(t, srv.URL+"/api/tools/send_email", `{"to":["rakshit@example.com"],"subject":"Run","body":"See you.\nBest,\n[Your Name]"}`, &out)
	if !strings.Contains(out.Error, "[Your Name]") {
		t.Fatalf("placeholder should be refused: %+v", out)
	}
}

func TestSendDraftCleansOldDraftsFirst(t *testing.T) {
	ws := mockWorkspace()
	id, _ := ws.Mail().CreateDraft(context.Background(), provider.OutgoingEmail{To: []string{"r@example.com"}, Subject: "Run", Body: `Hi,\n\nSee you.\nSanjay`})
	tools := NewToolbox(NewActionStore(), true, demoPlugins(ws, time.UTC)...)
	ctx := auth.WithUser(context.Background(), store.User{ID: "u1"})
	if _, err := tools.Call(ctx, "send_draft", json.RawMessage(`{"draft_id":"`+id+`"}`)); err != nil {
		t.Fatal(err)
	}
	d, _ := ws.Mail().GetDraft(context.Background(), id)
	if d.Message.Body != "Hi,\n\nSee you.\nSanjay" {
		t.Fatalf("old draft should be cleaned before it is queued: %q", d.Message.Body)
	}
}
