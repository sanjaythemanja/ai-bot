package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Job statuses. A job is one chat request running in the background.
const (
	JobRunning     = "running"
	JobDone        = "done"
	JobFailed      = "failed"
	JobCancelled   = "cancelled"
	JobInterrupted = "interrupted" // the server restarted while it ran
)

// ErrJobNotFound is returned for an unknown job, or one owned by someone else.
var ErrJobNotFound = errors.New("job not found")

// Job is a background chat run. Data holds the caller's full snapshot
// (steps, reply, …) as JSON; the store only needs the columns to query it.
type Job struct {
	ID             string
	UserID         string
	ConversationID string
	Status         string
	Data           json.RawMessage
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Jobs persists job snapshots so progress survives reloads and a restart
// can be detected. Every method is scoped to the owning user.
type Jobs interface {
	// SaveJob inserts or replaces the job's snapshot.
	SaveJob(ctx context.Context, j Job) error
	GetJob(ctx context.Context, userID, id string) (Job, error)
	// LatestJob returns the newest job in a conversation, or ErrJobNotFound.
	LatestJob(ctx context.Context, userID, conversationID string) (Job, error)
	// InterruptRunning marks jobs left running by a previous process.
	InterruptRunning(ctx context.Context) (int, error)
}
