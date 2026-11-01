package assistant

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/go-kit/log"

	"github.com/sanjayrj/ai-bot/internal/auth"
	"github.com/sanjayrj/ai-bot/internal/llm"
	"github.com/sanjayrj/ai-bot/internal/plugin"
	calendarplugin "github.com/sanjayrj/ai-bot/internal/plugins/calendar"
	gmailplugin "github.com/sanjayrj/ai-bot/internal/plugins/gmail"
	meetplugin "github.com/sanjayrj/ai-bot/internal/plugins/meet"
	slackplugin "github.com/sanjayrj/ai-bot/internal/plugins/slack"
	"github.com/sanjayrj/ai-bot/internal/provider"
	"github.com/sanjayrj/ai-bot/internal/provider/mock"
	"github.com/sanjayrj/ai-bot/internal/store"
)

// Both fakes script the same two-step conversation: search mail and queue an
// email, then answer in text. The second request must carry both results.

func fakeOpenAI(t *testing.T) (*httptest.Server, *atomic.Int32) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("unexpected request %s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		body, _ := io.ReadAll(r.Body)
		var msg string
		if calls.Add(1) == 1 {
			if !strings.Contains(string(body), `"role":"system"`) || !strings.Contains(string(body), `"type":"function"`) {
				t.Errorf("first request missing system prompt or tools: %s", body)
			}
			msg = `{"role":"assistant","content":null,"tool_calls":[
				{"id":"call_1","type":"function","function":{"name":"search_emails","arguments":"{\"query\":\"is:unread renewal\"}"}},
				{"id":"call_2","type":"function","function":{"name":"send_email","arguments":"{\"to\":[\"dana@example.com\"],\"body\":\"Happy to set up a call.\",\"reply_to_id\":\"m4\"}"}}]}`
			fmt.Fprintf(w, `{"choices":[{"message":%s,"finish_reason":"tool_calls"}]}`, msg)
			return
		}
		if !strings.Contains(string(body), `"tool_call_id":"call_1"`) || !strings.Contains(string(body), `"tool_call_id":"call_2"`) {
			t.Errorf("second request missing tool results: %s", body)
		}
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"Dana wants a call; I queued a reply for your approval."},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func fakeAnthropic(t *testing.T) (*httptest.Server, *atomic.Int32) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n := calls.Add(1)
		content, stop := `[{"type":"text","text":"Dana wants a call; I queued a reply for your approval."}]`, "end_turn"
		if n == 1 {
			content = `[{"type":"tool_use","id":"tu_1","name":"search_emails","input":{"query":"is:unread renewal"}},
				{"type":"tool_use","id":"tu_2","name":"send_email","input":{"to":["dana@example.com"],"body":"Happy to set up a call.","reply_to_id":"m4"}}]`
			stop = "tool_use"
		} else if !strings.Contains(string(body), `"tool_use_id":"tu_1"`) || !strings.Contains(string(body), `"tool_use_id":"tu_2"`) {
			t.Errorf("second request missing tool results: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"msg_%d","type":"message","role":"assistant","model":"m","content":%s,"stop_reason":"%s","stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}`, n, content, stop)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func newTestServer(t *testing.T, model llm.Client) *httptest.Server {
	return newTestServerWith(t, model, true)
}

func newTestServerWith(t *testing.T, model llm.Client, requireApproval bool) *httptest.Server {
	return serveAs(t, newTestHandler(model, requireApproval), "u1")
}

// newTestHandler builds the full API on demo data; serveAs puts a signed-in
// user in front of it, so several users can share one backend.
func newTestHandler(model llm.Client, requireApproval bool) http.Handler {
	loc := time.UTC
	ws := mock.New(time.Now().In(loc))
	actions := NewActionStore()
	tools := NewToolbox(actions, requireApproval, demoPlugins(ws, loc)...)
	svc := NewService(Config{
		Mail: ws.Mail(), Calendar: ws.Calendar(), Agent: NewAgent(model, tools, loc, nil), Tools: tools, Actions: actions,
		Location: loc, DemoMode: true, AIConfigured: model != nil, AIProvider: "test", Model: "test-model",
	})
	mux := http.NewServeMux()
	RegisterHTTPRoutes(mux, MakeEndpoints(svc, log.NewNopLogger()), log.NewNopLogger())
	return mux
}

func serveAs(t *testing.T, h http.Handler, userID string) *httptest.Server {
	u := store.User{ID: userID, Email: userID + "@example.com"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(auth.WithUser(r.Context(), u)))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func postJSON(t *testing.T, url, body string, out any) int {
	t.Helper()
	res, err := http.Post(url, "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil {
		_ = json.NewDecoder(res.Body).Decode(out)
	}
	return res.StatusCode
}

func getJSON(t *testing.T, url string, out any) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	_ = json.NewDecoder(res.Body).Decode(out)
}

func TestChatFlowWithEachProvider(t *testing.T) {
	cases := map[string]func(t *testing.T) (llm.Client, *atomic.Int32){
		"openai-compatible": func(t *testing.T) (llm.Client, *atomic.Int32) {
			srv, calls := fakeOpenAI(t)
			return llm.NewOpenAI("test-key", "any-model", srv.URL), calls
		},
		"anthropic": func(t *testing.T) (llm.Client, *atomic.Int32) {
			srv, calls := fakeAnthropic(t)
			return llm.NewAnthropic("test-key", "any-model", srv.URL, option.WithMaxRetries(0)), calls
		},
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			model, calls := mk(t)
			srv := newTestServer(t, model)

			var chat ChatReply
			if code := postJSON(t, srv.URL+"/api/chat", `{"session_id":"s1","message":"Handle Dana's email"}`, &chat); code != 200 {
				t.Fatalf("chat status %d", code)
			}
			if calls.Load() != 2 {
				t.Fatalf("expected 2 model calls, got %d", calls.Load())
			}
			if !strings.Contains(chat.Reply, "queued") || len(chat.ToolCalls) != 2 {
				t.Fatalf("unexpected reply: %+v", chat)
			}
			if chat.ToolCalls[0].IsError || !strings.Contains(chat.ToolCalls[0].Output, "Renewal concerns") {
				t.Fatalf("search_emails result wrong: %+v", chat.ToolCalls[0])
			}

			var list struct{ Actions []Action }
			getJSON(t, srv.URL+"/api/actions", &list)
			if len(list.Actions) != 1 || list.Actions[0].Status != StatusPending || list.Actions[0].Kind != ActionSendEmail {
				t.Fatalf("expected one pending email action, got %+v", list.Actions)
			}
			id := list.Actions[0].ID

			var approved struct{ Action Action }
			if code := postJSON(t, srv.URL+"/api/actions/"+id+"/approve", "{}", &approved); code != 200 || approved.Action.Status != StatusExecuted {
				t.Fatalf("approve: code %d action %+v", code, approved.Action)
			}
			if code := postJSON(t, srv.URL+"/api/actions/"+id+"/approve", "{}", nil); code != http.StatusConflict {
				t.Fatalf("expected 409 on double approve, got %d", code)
			}
		})
	}
}

func TestToolsAPIForExternalAgents(t *testing.T) {
	srv := newTestServer(t, nil)

	var list struct{ Tools []ToolInfo }
	getJSON(t, srv.URL+"/api/tools", &list)
	names := map[string]ToolInfo{}
	for _, tl := range list.Tools {
		names[tl.Name] = tl
	}
	for _, want := range []string{"send_email", "update_event", "delete_event", "update_draft", "send_draft", "edit_pending_action"} {
		if _, ok := names[want]; !ok {
			t.Fatalf("missing tool %s", want)
		}
	}
	if !names["send_email"].NeedsApproval || !names["update_event"].NeedsApproval || names["search_emails"].NeedsApproval || names["update_draft"].NeedsApproval {
		t.Fatalf("unexpected tools: %+v", list.Tools)
	}
	if names["search_emails"].InputSchema["type"] != "object" {
		t.Fatalf("schema not an object: %+v", names["search_emails"].InputSchema)
	}

	var out ToolOutput
	if code := postJSON(t, srv.URL+"/api/tools/search_emails", `{"query":"term sheet"}`, &out); code != 200 || !strings.Contains(string(out.Result), "Priya") {
		t.Fatalf("search via tools API: %d %s %s", code, out.Result, out.Error)
	}
	// Writes from external agents are queued, not executed.
	if code := postJSON(t, srv.URL+"/api/tools/create_event", `{"title":"Sync","start":"2030-01-01T10:00:00Z","end":"2030-01-01T10:30:00Z"}`, &out); code != 200 ||
		!strings.Contains(string(out.Result), "awaiting_user_approval") {
		t.Fatalf("create_event via tools API: %d %s %s", code, out.Result, out.Error)
	}
	if code := postJSON(t, srv.URL+"/api/tools/create_event", `{"title":"Bad","start":"x","end":"y"}`, &out); code != 200 || out.Error == "" {
		t.Fatalf("expected tool error in body, got %d %+v", code, out)
	}
	if code := postJSON(t, srv.URL+"/api/tools/nope", `{}`, nil); code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown tool, got %d", code)
	}
}

func TestChatWithoutModelAndOverview(t *testing.T) {
	srv := newTestServer(t, nil)
	if code := postJSON(t, srv.URL+"/api/chat", `{"message":"hi"}`, nil); code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 without a model, got %d", code)
	}
	if code := postJSON(t, srv.URL+"/api/chat", `{"message":"  "}`, nil); code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty message, got %d", code)
	}
	var ov Overview
	getJSON(t, srv.URL+"/api/overview", &ov)
	if len(ov.Emails) == 0 || len(ov.Events) == 0 || ov.Errors != nil {
		t.Fatalf("overview incomplete: %+v", ov)
	}
}

func TestApprovalOffRunsWritesImmediately(t *testing.T) {
	srv := newTestServerWith(t, nil, false)

	var st Status
	getJSON(t, srv.URL+"/api/status", &st)
	if st.RequireApproval {
		t.Fatal("status should report require_approval=false")
	}
	var list struct{ Tools []ToolInfo }
	getJSON(t, srv.URL+"/api/tools", &list)
	for _, tl := range list.Tools {
		if tl.NeedsApproval {
			t.Fatalf("%s should not need approval in auto mode", tl.Name)
		}
		if tl.Name == "create_event" && !strings.Contains(tl.Description, "runs immediately") {
			t.Fatalf("description not adapted: %q", tl.Description)
		}
	}

	var out ToolOutput
	if code := postJSON(t, srv.URL+"/api/tools/create_event", `{"title":"Focus","start":"2030-01-01T10:00:00Z","end":"2030-01-01T10:30:00Z"}`, &out); code != 200 ||
		!strings.Contains(string(out.Result), `"status":"done"`) || !strings.Contains(string(out.Result), "Event created") {
		t.Fatalf("expected immediate execution: %d %s %s", code, out.Result, out.Error)
	}

	var actions struct{ Actions []Action }
	getJSON(t, srv.URL+"/api/actions", &actions)
	if len(actions.Actions) != 1 || actions.Actions[0].Status != StatusExecuted {
		t.Fatalf("expected one executed action in the log, got %+v", actions.Actions)
	}
	// Already executed, so a late approve must not run it again.
	if code := postJSON(t, srv.URL+"/api/actions/"+actions.Actions[0].ID+"/approve", "{}", nil); code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", code)
	}
}

// call invokes a tool over HTTP and returns the decoded result object.
func call(t *testing.T, srv *httptest.Server, name, input string) map[string]any {
	t.Helper()
	var out ToolOutput
	if code := postJSON(t, srv.URL+"/api/tools/"+name, input, &out); code != 200 || out.Error != "" {
		t.Fatalf("%s(%s): code %d error %q", name, input, code, out.Error)
	}
	var m map[string]any
	if err := json.Unmarshal(out.Result, &m); err != nil {
		t.Fatalf("%s result is not an object: %s", name, out.Result)
	}
	return m
}

func approve(t *testing.T, srv *httptest.Server, id string) Action {
	t.Helper()
	var r struct{ Action Action }
	if code := postJSON(t, srv.URL+"/api/actions/"+id+"/approve", "{}", &r); code != 200 || r.Action.Status != StatusExecuted {
		t.Fatalf("approve %s: code %d %+v", id, code, r.Action)
	}
	return r.Action
}

func TestUpdateEventKeepsDurationAndAddsAttendee(t *testing.T) {
	srv := newTestServer(t, nil)
	// e2 in the mock: "1:1 with Sara", 11:00–11:30 today.
	res := call(t, srv, "update_event", `{"event_id":"e2","start":"2030-01-02T14:00:00Z","location":"Room 4","add_attendees":["new@example.com"]}`)
	if res["status"] != "awaiting_user_approval" {
		t.Fatalf("expected queued update, got %v", res)
	}
	sum := res["summary"].(string)
	if !strings.Contains(sum, "+new@example.com") || !strings.Contains(sum, "Room 4") || !strings.Contains(sum, "time →") {
		t.Fatalf("summary should describe the change: %q", sum)
	}
	var list struct{ Actions []Action }
	getJSON(t, srv.URL+"/api/actions", &list)
	var queued struct {
		Before *provider.Event `json:"before"`
	}
	_ = json.Unmarshal(list.Actions[0].Payload, &queued)
	if queued.Before == nil || queued.Before.Location != "" || len(queued.Before.Attendees) != 1 {
		t.Fatalf("update payload should snapshot the event before the change: %s", list.Actions[0].Payload)
	}
	approve(t, srv, res["action_id"].(string))

	ev := call(t, srv, "get_event", `{"event_id":"e2"}`)
	start, _ := time.Parse(time.RFC3339, ev["start"].(string))
	end, _ := time.Parse(time.RFC3339, ev["end"].(string))
	if end.Sub(start) != 30*time.Minute || ev["location"] != "Room 4" {
		t.Fatalf("duration or location wrong: %v", ev)
	}
	if !strings.Contains(fmt.Sprint(ev["attendees"]), "new@example.com") || !strings.Contains(fmt.Sprint(ev["attendees"]), "sara@example.com") {
		t.Fatalf("attendees should keep Sara and add the new one: %v", ev["attendees"])
	}
}

func TestEditPendingEmailAddsBccAndValidatesFields(t *testing.T) {
	srv := newTestServer(t, nil)
	res := call(t, srv, "send_email", `{"to":["dana@example.com"],"subject":"Renewal","body":"Let's talk."}`)
	id := res["action_id"].(string)

	edited := call(t, srv, "edit_pending_action", `{"action_id":"`+id+`","add_bcc":["boss@example.com"],"subject":"Renewal call","add_cc":["sara@example.com"]}`)
	if !strings.Contains(edited["summary"].(string), "bcc boss@example.com") || !strings.Contains(edited["summary"].(string), "Renewal call") {
		t.Fatalf("pending email not updated: %v", edited)
	}

	var out ToolOutput
	postJSON(t, srv.URL+"/api/tools/edit_pending_action", `{"action_id":"`+id+`","location":"x"}`, &out)
	if !strings.Contains(out.Error, "do not apply") {
		t.Fatalf("expected field validation error, got %+v", out)
	}

	a := approve(t, srv, id)
	var sent provider.OutgoingEmail
	_ = json.Unmarshal(a.Payload, &sent)
	if len(sent.Bcc) != 1 || len(sent.Cc) != 1 || sent.Subject != "Renewal call" {
		t.Fatalf("sent email lost edits: %+v", sent)
	}
}

func TestDraftEditSendAndEventDelete(t *testing.T) {
	srv := newTestServer(t, nil)
	d := call(t, srv, "draft_email", `{"to":["priya@example.com"],"subject":"Term sheet","body":"Draft"}`)
	draftID := d["draft_id"].(string)

	call(t, srv, "update_draft", `{"draft_id":"`+draftID+`","add_cc":["legal@example.com"],"body":"Final wording"}`)
	got := call(t, srv, "read_draft", `{"draft_id":"`+draftID+`"}`)
	if !strings.Contains(fmt.Sprint(got["message"]), "legal@example.com") || !strings.Contains(fmt.Sprint(got["message"]), "Final wording") {
		t.Fatalf("draft not updated: %v", got)
	}

	q := call(t, srv, "send_draft", `{"draft_id":"`+draftID+`"}`)
	approve(t, srv, q["action_id"].(string))
	var out ToolOutput
	postJSON(t, srv.URL+"/api/tools/read_draft", `{"draft_id":"`+draftID+`"}`, &out)
	if out.Error == "" {
		t.Fatal("sent draft should be gone")
	}

	del := call(t, srv, "delete_event", `{"event_id":"e4"}`)
	if !strings.Contains(del["summary"].(string), "Cancel") {
		t.Fatalf("unexpected delete summary: %v", del)
	}
	approve(t, srv, del["action_id"].(string))
	postJSON(t, srv.URL+"/api/tools/get_event", `{"event_id":"e4"}`, &out)
	if out.Error == "" {
		t.Fatal("deleted event should be gone")
	}
}

func TestConversationsPersistAndList(t *testing.T) {
	claude, calls := fakeOpenAI(t)
	srv := newTestServer(t, llm.NewOpenAI("test-key", "m", claude.URL))

	postJSON(t, srv.URL+"/api/chat", `{"session_id":"c1","message":"Handle Dana's email"}`, nil)
	var second ChatReply
	if code := postJSON(t, srv.URL+"/api/chat", `{"session_id":"c1","message":"thanks"}`, &second); code != 200 {
		t.Fatalf("second chat status %d", code)
	}
	if calls.Load() != 3 {
		t.Fatalf("expected 3 model calls across two turns, got %d", calls.Load())
	}

	var list struct {
		Conversations []struct {
			ID, Title string
			Turns     int
		}
	}
	getJSON(t, srv.URL+"/api/conversations", &list)
	if len(list.Conversations) != 1 || list.Conversations[0].Turns != 2 || list.Conversations[0].Title != "Handle Dana's email" {
		t.Fatalf("unexpected conversation list: %+v", list.Conversations)
	}

	var conv struct {
		Turns []struct {
			UserText  string `json:"user_text"`
			Reply     string
			ToolCalls []ToolCall `json:"tool_calls"`
		}
	}
	getJSON(t, srv.URL+"/api/conversations/c1", &conv)
	if len(conv.Turns) != 2 || conv.Turns[0].UserText != "Handle Dana's email" || len(conv.Turns[0].ToolCalls) != 2 || conv.Turns[1].UserText != "thanks" {
		t.Fatalf("unexpected transcript: %+v", conv.Turns)
	}

	if code := postJSON(t, srv.URL+"/api/conversations/c1/delete", "{}", nil); code != 200 {
		t.Fatalf("delete status %d", code)
	}
	res, _ := http.Get(srv.URL + "/api/conversations/c1")
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", res.StatusCode)
	}
}

func TestColleagueAvailabilityAndCalendars(t *testing.T) {
	srv := newTestServer(t, nil)
	day := time.Now().UTC().Format("2006-01-02")
	tomorrow := time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02")

	// 14:15–14:45 today: Sara (incident review) and Marcus (finance close) are busy.
	var out ToolOutput
	postJSON(t, srv.URL+"/api/tools/check_availability",
		`{"people":["sara@example.com","marcus@example.com","outsider@other.org"],"start":"`+day+`T14:15:00Z","end":"`+day+`T14:45:00Z"}`, &out)
	var views []struct {
		Person string
		Free   *bool
		Error  string
	}
	if err := json.Unmarshal(out.Result, &views); err != nil || len(views) != 3 {
		t.Fatalf("availability: %s %s", out.Result, out.Error)
	}
	if *views[0].Free || *views[1].Free || views[2].Error == "" || views[2].Free != nil {
		t.Fatalf("expected Sara and Marcus busy, outsider unknown: %+v", views)
	}

	// Tomorrow: Sara busy 10–11, so a common 60-minute slot starts at 09:00 and 11:00.
	slots := call(t, srv, "find_common_free_slots",
		`{"people":["sara@example.com","outsider@other.org"],"date":"`+tomorrow+`","duration_minutes":60}`)
	var starts []string
	for _, sl := range slots["slots"].([]any) {
		starts = append(starts, sl.(map[string]any)["start"].(string)[11:16])
	}
	if strings.Join(starts, ",") != "09:00,11:00" {
		t.Fatalf("unexpected common slots: %v", slots["slots"])
	}
	if _, ok := slots["unknown"].(map[string]any)["outsider@other.org"]; !ok {
		t.Fatalf("unreadable calendar should be reported, not treated as free: %v", slots)
	}

	// Sara shares details; Marcus shares only free/busy.
	var saraEvents []map[string]any
	postJSON(t, srv.URL+"/api/tools/list_events", `{"calendar":"sara@example.com","start":"`+day+`T00:00:00Z","end":"`+day+`T23:59:00Z"}`, &out)
	if err := json.Unmarshal(out.Result, &saraEvents); err != nil || !strings.Contains(fmt.Sprint(saraEvents), "Incident review") {
		t.Fatalf("Sara's shared calendar should list her meetings: %s %s", out.Result, out.Error)
	}
	postJSON(t, srv.URL+"/api/tools/list_events", `{"calendar":"marcus@example.com","start":"`+day+`T00:00:00Z","end":"`+day+`T23:59:00Z"}`, &out)
	if !strings.Contains(out.Error, "not shared") {
		t.Fatalf("expected not-shared error for Marcus, got %+v", out)
	}
}

func TestMeetingTranscript(t *testing.T) {
	srv := newTestServer(t, nil)
	tr := call(t, srv, "get_meeting_transcript", `{"event_id":"e1"}`)
	if !strings.Contains(tr["transcript"].(string), "Decision: CEO joins") || tr["meeting_code"] != "abc-defg-hij" {
		t.Fatalf("unexpected transcript: %v", tr)
	}
	var out ToolOutput
	postJSON(t, srv.URL+"/api/tools/get_meeting_transcript", `{"event_id":"e2"}`, &out)
	if !strings.Contains(out.Error, "no transcript") {
		t.Fatalf("expected no-transcript error, got %+v", out)
	}
	postJSON(t, srv.URL+"/api/tools/get_meeting_transcript", `{"event_id":"e3"}`, &out)
	if !strings.Contains(out.Error, "no Google Meet link") {
		t.Fatalf("expected no-meet-link error, got %+v", out)
	}
}

func TestUsersAreIsolated(t *testing.T) {
	model, _ := fakeOpenAI(t)
	h := newTestHandler(llm.NewOpenAI("test-key", "m", model.URL), true)
	alice, bob := serveAs(t, h, "alice"), serveAs(t, h, "bob")

	// Alice chats; the fake model queues an email for her.
	if code := postJSON(t, alice.URL+"/api/chat", `{"session_id":"shared-id","message":"Handle Dana's email"}`, nil); code != 200 {
		t.Fatalf("alice chat: %d", code)
	}
	var aliceActions struct{ Actions []Action }
	getJSON(t, alice.URL+"/api/actions", &aliceActions)
	if len(aliceActions.Actions) != 1 {
		t.Fatalf("alice should have 1 action, got %d", len(aliceActions.Actions))
	}
	actionID := aliceActions.Actions[0].ID

	// Bob sees none of it.
	var bobConvs struct{ Conversations []store.Summary }
	getJSON(t, bob.URL+"/api/conversations", &bobConvs)
	var bobActions struct{ Actions []Action }
	getJSON(t, bob.URL+"/api/actions", &bobActions)
	if len(bobConvs.Conversations) != 0 || len(bobActions.Actions) != 0 {
		t.Fatalf("bob sees alice's data: %+v %+v", bobConvs, bobActions)
	}
	res, _ := http.Get(bob.URL + "/api/conversations/shared-id")
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("bob read alice's conversation: %d", res.StatusCode)
	}
	if code := postJSON(t, bob.URL+"/api/actions/"+actionID+"/approve", "{}", nil); code != http.StatusNotFound {
		t.Fatalf("bob approved alice's action: %d", code)
	}
	if code := postJSON(t, bob.URL+"/api/conversations/shared-id/delete", "{}", nil); code != http.StatusNotFound {
		t.Fatalf("bob deleted alice's conversation: %d", code)
	}

	// Alice's action is untouched and still hers to approve.
	if code := postJSON(t, alice.URL+"/api/actions/"+actionID+"/approve", "{}", nil); code != 200 {
		t.Fatalf("alice approve: %d", code)
	}
}

func TestRequestsWithoutUserAreRejected(t *testing.T) {
	srv := httptest.NewServer(newTestHandler(nil, true))
	t.Cleanup(srv.Close)
	res, err := http.Get(srv.URL + "/api/conversations")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 with no signed-in user, got %d", res.StatusCode)
	}
}

func TestDuplicateActionsAreRefused(t *testing.T) {
	srv := newTestServer(t, nil)
	first := call(t, srv, "send_email", `{"to":["typo@example.com"],"subject":"Join the call","body":"Please join at 3."}`)
	id := first["action_id"].(string)

	// Same email with the recipient "fixed" must not create a second action.
	var out ToolOutput
	postJSON(t, srv.URL+"/api/tools/send_email", `{"to":["right@example.com"],"subject":"Join the call","body":"Please join at 3."}`, &out)
	if !strings.Contains(out.Error, id) || !strings.Contains(out.Error, "edit_pending_action") {
		t.Fatalf("expected duplicate refusal pointing at %s, got %+v", id, out)
	}
	// The intended fix works and leaves exactly one action.
	call(t, srv, "edit_pending_action", `{"action_id":"`+id+`","to":["right@example.com"]}`)
	var list struct{ Actions []Action }
	getJSON(t, srv.URL+"/api/actions", &list)
	if len(list.Actions) != 1 || !strings.Contains(list.Actions[0].Summary, "right@example.com") {
		t.Fatalf("expected one corrected action, got %+v", list.Actions)
	}

	// A genuinely different email is fine.
	call(t, srv, "send_email", `{"to":["other@example.com"],"subject":"Lunch?","body":"Free Friday?"}`)

	// Same event twice is refused; a second change to one event is refused.
	call(t, srv, "create_event", `{"title":"Sync","start":"2030-01-01T10:00:00Z","end":"2030-01-01T10:30:00Z"}`)
	postJSON(t, srv.URL+"/api/tools/create_event", `{"title":"sync","start":"2030-01-01T10:15:00Z","end":"2030-01-01T10:45:00Z"}`, &out)
	if !strings.Contains(out.Error, "already waiting") {
		t.Fatalf("expected duplicate event refusal, got %+v", out)
	}
	call(t, srv, "update_event", `{"event_id":"e2","location":"Room 1"}`)
	postJSON(t, srv.URL+"/api/tools/update_event", `{"event_id":"e2","location":"Room 2"}`, &out)
	if !strings.Contains(out.Error, "already waiting") {
		t.Fatalf("expected refusal of second pending change to e2, got %+v", out)
	}
}

// demoPlugins wires the standard plugins to the demo workspace.
func demoPlugins(ws *mock.Workspace, loc *time.Location) []plugin.Plugin {
	settings := UserSettings{Store: store.NewMemory()}
	return []plugin.Plugin{
		gmailplugin.New(ws.Mail(), settings),
		calendarplugin.New(ws.Calendar(), loc, settings),
		meetplugin.New(ws.Meetings(), ws.Calendar(), loc),
		slackplugin.New(ws.Chat()),
	}
}

func TestSlackPluginFlow(t *testing.T) {
	loc := time.UTC
	ws := mock.New(time.Now().In(loc))
	actions := NewActionStore()
	tools := NewToolbox(actions, true, demoPlugins(ws, loc)...)
	svc := NewService(Config{Mail: ws.Mail(), Calendar: ws.Calendar(), Agent: NewAgent(nil, tools, loc, nil), Tools: tools, Actions: actions, Location: loc})
	mux := http.NewServeMux()
	RegisterHTTPRoutes(mux, MakeEndpoints(svc, log.NewNopLogger()), log.NewNopLogger())
	srv := serveAs(t, mux, "u1")

	// Reads work and Slack has no email tools.
	var mentions ToolOutput
	postJSON(t, srv.URL+"/api/tools/slack_mentions", `{}`, &mentions)
	if mentions.Error != "" || !strings.Contains(string(mentions.Result), "hiring plan") {
		t.Fatalf("mentions: %s %s", mentions.Result, mentions.Error)
	}
	var list struct{ Tools []ToolInfo }
	getJSON(t, srv.URL+"/api/tools", &list)
	for _, tl := range list.Tools {
		if strings.Contains(tl.Name, "slack") && (strings.Contains(tl.Name, "email") || strings.Contains(tl.Name, "draft")) {
			t.Fatalf("slack plugin should not expose mail tools: %s", tl.Name)
		}
	}

	// Unknown channel is rejected before anything is queued.
	var out ToolOutput
	postJSON(t, srv.URL+"/api/tools/post_slack_message", `{"channel":"#nope","text":"hi"}`, &out)
	if !strings.Contains(out.Error, "no channel") {
		t.Fatalf("expected unknown-channel error, got %+v", out)
	}

	// Post is queued with the resolved channel name.
	q := call(t, srv, "post_slack_message", `{"channel":"exec-team","text":"Numbers by Wednesday 👍"}`)
	id := q["action_id"].(string)
	if !strings.Contains(q["summary"].(string), "#exec-team") {
		t.Fatalf("summary should name the channel: %v", q)
	}

	// A "fixed" copy to another channel is refused; editing works instead.
	postJSON(t, srv.URL+"/api/tools/post_slack_message", `{"channel":"incidents","text":"Numbers by Wednesday 👍"}`, &out)
	if !strings.Contains(out.Error, id) {
		t.Fatalf("expected duplicate refusal, got %+v", out)
	}
	edited := call(t, srv, "edit_pending_action", `{"action_id":"`+id+`","channel":"incidents","text":"Numbers by Thursday"}`)
	if !strings.Contains(edited["summary"].(string), "#incidents") {
		t.Fatalf("edit not applied: %v", edited)
	}
	postJSON(t, srv.URL+"/api/tools/edit_pending_action", `{"action_id":"`+id+`","subject":"x"}`, &out)
	if !strings.Contains(out.Error, "do not apply") {
		t.Fatalf("email fields must not apply to slack posts: %+v", out)
	}

	approve(t, srv, id)
	posted := ws.Posted()
	if len(posted) != 1 || posted[0].ChannelName != "incidents" || posted[0].Text != "Numbers by Thursday" {
		t.Fatalf("expected one edited post in #incidents, got %+v", posted)
	}
}

func TestDuplicatePluginToolsPanic(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("registering two plugins with the same tool name must panic")
		}
	}()
	ws := mock.New(time.Now())
	NewToolbox(NewActionStore(), true, slackplugin.New(ws.Chat()), slackplugin.New(ws.Chat()))
}
