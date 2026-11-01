package assistant

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sanjayrj/ai-bot/internal/auth"
	"github.com/sanjayrj/ai-bot/internal/llm"
	"github.com/sanjayrj/ai-bot/internal/store"
)

// hangingModel blocks until the request is cancelled.
type hangingModel struct{ started chan struct{} }

func (m hangingModel) Complete(ctx context.Context, _ llm.Request) (llm.Response, error) {
	select {
	case m.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return llm.Response{}, ctx.Err()
}

func startJob(t *testing.T, url, session, msg string) Job {
	t.Helper()
	var r struct {
		Job   *Job
		Error string
	}
	if code := postJSON(t, url+"/api/chat/start", `{"session_id":"`+session+`","message":"`+msg+`"}`, &r); code != 200 || r.Job == nil {
		t.Fatalf("start: code %d %s", code, r.Error)
	}
	return *r.Job
}

func waitJob(t *testing.T, url, id string) Job {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var r struct{ Job Job }
		getJSON(t, url+"/api/jobs/"+id, &r)
		if r.Job.Finished() {
			return r.Job
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("job did not finish")
	return Job{}
}

func TestBackgroundJobRunsAndStreams(t *testing.T) {
	day := time.Now().UTC().Format("2006-01-02")
	m := &scriptModel{turns: []llm.Response{toolCalls(
		[2]string{"list_events", `{"start":"` + day + `T00:00:00Z","end":"` + day + `T23:59:00Z"}`},
		[2]string{"search_emails", `{"query":"is:unread"}`})}}
	srv := newTestServer(t, m)

	j := startJob(t, srv.URL, "conv-1", "what is on today")
	if j.Status != store.JobRunning || j.ID == "" {
		t.Fatalf("start should return a running job: %+v", j)
	}

	// The event stream sends snapshots and ends with the finished job.
	res, err := http.Get(srv.URL + "/api/jobs/" + j.ID + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	var last Job
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
			_ = json.Unmarshal([]byte(data), &last)
		}
	}
	if last.Status != store.JobDone || last.Reply != "done" {
		t.Fatalf("stream should end with the finished job: %+v", last)
	}
	tools := 0
	for _, s := range last.Steps {
		if s.Kind == StepTool && s.Status == StepDone {
			tools++
		}
	}
	if tools != 2 || len(last.ToolCalls) != 2 {
		t.Fatalf("expected 2 finished tool steps, got %+v", last.Steps)
	}

	// The turn was saved to the conversation, and the job can be found again.
	var conv struct{ Turns []store.Turn }
	getJSON(t, srv.URL+"/api/conversations/conv-1", &conv)
	if len(conv.Turns) != 1 || conv.Turns[0].Reply != "done" {
		t.Fatalf("turn not saved: %+v", conv.Turns)
	}
	var cj struct{ Job *Job }
	getJSON(t, srv.URL+"/api/conversations/conv-1/job", &cj)
	if cj.Job == nil || cj.Job.ID != j.ID || cj.Job.Status != store.JobDone {
		t.Fatalf("conversation job: %+v", cj.Job)
	}
}

func TestJobCancelAndOnePerConversation(t *testing.T) {
	m := hangingModel{started: make(chan struct{}, 1)}
	srv := newTestServer(t, m)
	j := startJob(t, srv.URL, "conv-2", "slow request")
	<-m.started

	var out struct{ Error string }
	if code := postJSON(t, srv.URL+"/api/chat/start", `{"session_id":"conv-2","message":"another"}`, &out); code != http.StatusConflict {
		t.Fatalf("a second message while running should be 409, got %d", code)
	}
	if code := postJSON(t, srv.URL+"/api/jobs/"+j.ID+"/cancel", `{}`, nil); code != 200 {
		t.Fatalf("cancel: %d", code)
	}
	done := waitJob(t, srv.URL, j.ID)
	if done.Status != store.JobCancelled {
		t.Fatalf("expected cancelled, got %+v", done)
	}
	var conv struct{ Turns []store.Turn }
	getJSON(t, srv.URL+"/api/conversations/conv-2", &conv)
	if len(conv.Turns) != 1 || conv.Turns[0].Error != "Stopped before finishing." {
		t.Fatalf("stopped turn should be saved: %+v", conv.Turns)
	}
	// Free again once stopped.
	startJob(t, srv.URL, "conv-2", "try again")
}

func TestJobsAreIsolatedAndRecovered(t *testing.T) {
	h := newTestHandler(&scriptModel{}, true)
	alice, bob := serveAs(t, h, "alice"), serveAs(t, h, "bob")
	j := startJob(t, alice.URL, "c", "hello")
	waitJob(t, alice.URL, j.ID)
	var out struct{ Error string }
	res, _ := http.Get(bob.URL + "/api/jobs/" + j.ID)
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("another user must not see the job: %d", res.StatusCode)
	}
	if code := postJSON(t, bob.URL+"/api/jobs/"+j.ID+"/cancel", `{}`, &out); code != http.StatusNotFound {
		t.Fatalf("another user must not cancel the job: %d", code)
	}

	// A job left running by a previous process shows as interrupted.
	st := store.NewMemory()
	data, _ := json.Marshal(Job{ID: "j1", ConversationID: "c", Status: store.JobRunning})
	_ = st.SaveJob(context.Background(), store.Job{ID: "j1", UserID: "u", ConversationID: "c", Status: store.JobRunning, Data: data})
	js := NewJobs(st, nil)
	js.Recover(context.Background())
	got, err := js.get(auth.WithUser(context.Background(), store.User{ID: "u"}), "u", "j1")
	if err != nil || got.Status != store.JobInterrupted || !strings.Contains(got.Error, "restarted") {
		t.Fatalf("expected interrupted job, got %+v %v", got, err)
	}
}
