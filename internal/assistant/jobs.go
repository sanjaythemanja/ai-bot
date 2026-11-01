package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/go-kit/log"

	"github.com/sanjayrj/ai-bot/internal/store"
)

// Job is a chat request running in the background: the browser can watch
// its steps live, close the tab, and come back to the result.
type Job struct {
	ID             string     `json:"id"`
	ConversationID string     `json:"conversation_id"`
	Message        string     `json:"message"`
	Status         string     `json:"status"` // store.JobRunning, JobDone, …
	Steps          []Step     `json:"steps"`
	Reply          string     `json:"reply,omitempty"`
	ToolCalls      []ToolCall `json:"tool_calls,omitempty"`
	Warning        string     `json:"warning,omitempty"`
	Error          string     `json:"error,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// Finished reports whether the job has stopped, for any reason.
func (j Job) Finished() bool { return j.Status != store.JobRunning }

var (
	// ErrJobRunning means the conversation already has a job in progress.
	ErrJobRunning  = errors.New("this conversation is still working on the previous message")
	ErrJobNotFound = store.ErrJobNotFound
)

// runFunc runs one chat turn, reporting progress (bound to the service).
type runFunc func(ctx context.Context, sessionID, message string, progress func(Step)) (ChatReply, error)

// Jobs runs chat turns in the background, keeps their live state in memory
// for watchers, and saves snapshots so they survive reloads.
type Jobs struct {
	store   store.Jobs
	logger  log.Logger
	timeout time.Duration
	run     runFunc

	mu   sync.Mutex
	live map[string]*liveJob // by job ID
}

type liveJob struct {
	userID string
	cancel context.CancelFunc
	dirty  chan struct{} // wakes the saver

	mu   sync.Mutex
	job  Job
	subs map[chan Job]struct{}
}

// NewJobs returns a job runner backed by st. A job may run for at most 10
// minutes.
func NewJobs(st store.Jobs, logger log.Logger) *Jobs {
	if logger == nil {
		logger = log.NewNopLogger()
	}
	return &Jobs{store: st, logger: logger, timeout: 10 * time.Minute, live: map[string]*liveJob{}}
}

// Recover marks jobs that were running when the process last stopped.
func (js *Jobs) Recover(ctx context.Context) {
	if n, err := js.store.InterruptRunning(ctx); err != nil || n > 0 {
		_ = js.logger.Log("msg", "marked unfinished jobs as interrupted", "count", n, "err", err)
	}
}

// start launches a job for the signed-in user (in ctx). The job keeps the
// user but not ctx's cancellation, so it outlives the HTTP request.
func (js *Jobs) start(ctx context.Context, uid, sessionID, message string) (Job, error) {
	js.mu.Lock()
	for _, lj := range js.live {
		lj.mu.Lock()
		busy := lj.userID == uid && lj.job.ConversationID == sessionID
		lj.mu.Unlock()
		if busy {
			js.mu.Unlock()
			return Job{}, ErrJobRunning
		}
	}
	now := time.Now()
	j := Job{ID: store.NewID(), ConversationID: sessionID, Message: message, Status: store.JobRunning, Steps: []Step{}, CreatedAt: now, UpdatedAt: now}
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), js.timeout)
	lj := &liveJob{userID: uid, cancel: cancel, dirty: make(chan struct{}, 1), job: j, subs: map[chan Job]struct{}{}}
	js.live[j.ID] = lj
	js.mu.Unlock()

	if err := js.save(runCtx, lj); err != nil {
		js.forget(j.ID)
		cancel()
		return Job{}, err
	}
	saved := make(chan struct{})
	go js.saver(runCtx, lj, saved)
	go js.execute(runCtx, lj, sessionID, message, saved)
	return j, nil
}

func (js *Jobs) execute(ctx context.Context, lj *liveJob, sessionID, message string, saved <-chan struct{}) {
	defer lj.cancel()
	reply, err := js.run(ctx, sessionID, message, func(s Step) { lj.update(func(j *Job) { j.upsertStep(s) }) })

	lj.update(func(j *Job) {
		j.Reply, j.ToolCalls, j.Warning = reply.Reply, reply.ToolCalls, reply.Warning
		switch {
		case err == nil:
			j.Status = store.JobDone
		case errors.Is(err, context.Canceled):
			j.Status, j.Error = store.JobCancelled, "Stopped before finishing."
		case errors.Is(err, context.DeadlineExceeded):
			j.Status, j.Error = store.JobFailed, "This took longer than 10 minutes and was stopped."
		default:
			j.Status, j.Error = store.JobFailed, err.Error()
		}
		for i := range j.Steps { // nothing is still running once the job ends
			if s := &j.Steps[i]; s.Status == StepRunning || s.Status == StepSummarizing {
				s.Status = StepError
				if s.Error == "" {
					s.Error = "not finished"
				}
			}
		}
	})
	close(lj.dirty) // stops the saver after its last write
	<-saved
	if err := js.save(context.WithoutCancel(ctx), lj); err != nil {
		_ = js.logger.Log("msg", "saving finished job", "job", lj.job.ID, "err", err)
	}
	js.forget(lj.job.ID)
	lj.closeSubs()
}

// saver writes the latest snapshot whenever the job changes, coalescing
// bursts so parallel tool steps never wait on the database.
func (js *Jobs) saver(ctx context.Context, lj *liveJob, done chan<- struct{}) {
	defer close(done)
	for range lj.dirty {
		if err := js.save(context.WithoutCancel(ctx), lj); err != nil {
			_ = js.logger.Log("msg", "saving job progress", "job", lj.job.ID, "err", err)
		}
	}
}

func (js *Jobs) save(ctx context.Context, lj *liveJob) error {
	lj.mu.Lock()
	j := lj.job
	data, err := json.Marshal(j)
	lj.mu.Unlock()
	if err != nil {
		return err
	}
	return js.store.SaveJob(ctx, store.Job{ID: j.ID, UserID: lj.userID, ConversationID: j.ConversationID, Status: j.Status, Data: data})
}

func (js *Jobs) forget(id string) {
	js.mu.Lock()
	delete(js.live, id)
	js.mu.Unlock()
}

func (js *Jobs) liveFor(uid, id string) (*liveJob, bool) {
	js.mu.Lock()
	defer js.mu.Unlock()
	lj, ok := js.live[id]
	if !ok || lj.userID != uid {
		return nil, false
	}
	return lj, true
}

func (js *Jobs) get(ctx context.Context, uid, id string) (Job, error) {
	if lj, ok := js.liveFor(uid, id); ok {
		return lj.snapshot(), nil
	}
	rec, err := js.store.GetJob(ctx, uid, id)
	if err != nil {
		return Job{}, err
	}
	return decodeJob(rec)
}

func (js *Jobs) latest(ctx context.Context, uid, conversationID string) (*Job, error) {
	rec, err := js.store.LatestJob(ctx, uid, conversationID)
	if errors.Is(err, store.ErrJobNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	j, err := js.get(ctx, uid, rec.ID) // prefer the live copy
	return &j, err
}

func (js *Jobs) cancel(ctx context.Context, uid, id string) (Job, error) {
	if lj, ok := js.liveFor(uid, id); ok {
		lj.cancel()
		return lj.snapshot(), nil
	}
	return js.get(ctx, uid, id) // already finished: nothing to stop
}

// Watch returns the job now plus a channel of later snapshots, closed when
// the job ends. Call stop when done watching.
func (js *Jobs) Watch(ctx context.Context, id string) (Job, <-chan Job, func(), error) {
	uid, err := userID(ctx)
	if err != nil {
		return Job{}, nil, nil, err
	}
	if lj, ok := js.liveFor(uid, id); ok {
		ch := make(chan Job, 1)
		lj.mu.Lock()
		j := lj.job.clone()
		if lj.subs != nil {
			lj.subs[ch] = struct{}{}
		} else {
			close(ch)
		}
		lj.mu.Unlock()
		return j, ch, func() { lj.unsubscribe(ch) }, nil
	}
	j, err := js.get(ctx, uid, id)
	if err != nil {
		return Job{}, nil, nil, err
	}
	ch := make(chan Job)
	close(ch)
	return j, ch, func() {}, nil
}

func decodeJob(rec store.Job) (Job, error) {
	var j Job
	if err := json.Unmarshal(rec.Data, &j); err != nil {
		return Job{}, err
	}
	j.Status = rec.Status // the column wins (e.g. "interrupted" after a restart)
	if j.Status == store.JobInterrupted && j.Error == "" {
		j.Error = "The server restarted while this was running. Please send it again."
	}
	return j, nil
}

// ---- live job state ----

func (lj *liveJob) update(f func(*Job)) {
	lj.mu.Lock()
	f(&lj.job)
	lj.job.UpdatedAt = time.Now()
	j := lj.job.clone()
	for ch := range lj.subs {
		select { // keep only the newest snapshot per watcher
		case <-ch:
		default:
		}
		ch <- j
	}
	lj.mu.Unlock()
	select {
	case lj.dirty <- struct{}{}:
	default: // a save is already pending; it will pick this up
	}
}

func (lj *liveJob) snapshot() Job {
	lj.mu.Lock()
	defer lj.mu.Unlock()
	return lj.job.clone()
}

func (lj *liveJob) unsubscribe(ch chan Job) {
	lj.mu.Lock()
	defer lj.mu.Unlock()
	if _, ok := lj.subs[ch]; ok {
		delete(lj.subs, ch)
		close(ch)
	}
}

func (lj *liveJob) closeSubs() {
	lj.mu.Lock()
	defer lj.mu.Unlock()
	for ch := range lj.subs {
		select { // make sure the final state is what they read last
		case <-ch:
		default:
		}
		ch <- lj.job.clone()
		close(ch)
	}
	lj.subs = nil
}

// clone copies the job so a snapshot never shares slices with the live one.
func (j Job) clone() Job {
	j.Steps = append([]Step{}, j.Steps...)
	j.ToolCalls = append([]ToolCall(nil), j.ToolCalls...)
	return j
}

func (j *Job) upsertStep(s Step) {
	for i := range j.Steps {
		if j.Steps[i].ID == s.ID {
			j.Steps[i] = s
			return
		}
	}
	j.Steps = append(j.Steps, s)
}
