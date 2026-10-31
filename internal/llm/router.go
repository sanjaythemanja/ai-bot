package llm

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"sync"
	"time"
)

// ErrBusy means every configured model is overloaded or unreachable right
// now. Its text is written for end users.
var ErrBusy = errors.New("The AI model is busy right now because of high demand. Please try again in a minute.")

// Retryable reports whether err is worth retrying: rate limits, server
// errors and network failures, but not bad requests or a cancelled request.
func Retryable(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, ErrModelTimeout) {
		return true
	}
	var api *APIError
	if errors.As(err, &api) {
		return api.Status == 408 || api.Status == 429 || api.Status >= 500
	}
	var netErr net.Error
	return errors.As(err, &netErr) || errors.Is(err, context.DeadlineExceeded)
}

// retrying retries transient failures with exponential backoff and jitter.
// Each attempt has its own time limit; a model that hangs isn't retried, so
// the router can move to the next one quickly.
type retrying struct {
	next     Client
	attempts int
	base     time.Duration
	timeout  time.Duration
}

// AttemptTimeout caps one model call.
const AttemptTimeout = 60 * time.Second

// WithRetry wraps c so transient errors are retried up to attempts times.
func WithRetry(c Client, attempts int, base time.Duration) Client {
	return &retrying{next: c, attempts: attempts, base: base, timeout: AttemptTimeout}
}

// ErrModelTimeout means one model didn't answer within AttemptTimeout.
var ErrModelTimeout = errors.New("model did not respond in time")

func (r *retrying) Complete(ctx context.Context, req Request) (Response, error) {
	var err error
	for i := 0; i < r.attempts; i++ {
		var resp Response
		actx, cancel := context.WithTimeout(ctx, r.timeout)
		resp, err = r.next.Complete(actx, req)
		timedOut := err != nil && actx.Err() == context.DeadlineExceeded && ctx.Err() == nil
		cancel()
		if timedOut {
			return Response{}, ErrModelTimeout // hung: let the router try another model
		}
		if err == nil || !Retryable(err) {
			return resp, err
		}
		if i == r.attempts-1 {
			break
		}
		wait := r.base << i
		wait += time.Duration(rand.Int64N(int64(wait)/2 + 1)) // jitter
		select {
		case <-ctx.Done():
			return Response{}, ctx.Err()
		case <-time.After(wait):
		}
	}
	return Response{}, err
}

// Route is one model the router can use.
type Route struct {
	Name   string
	Client Client
}

// Router tries models in order. A model that fails with a transient error is
// skipped for a cooldown so later requests go straight to a working one.
type Router struct {
	routes   []Route
	cooldown time.Duration

	mu        sync.Mutex
	coolUntil map[string]time.Time
	now       func() time.Time
}

// NewRouter returns a Router over routes (most preferred first).
func NewRouter(cooldown time.Duration, routes ...Route) *Router {
	return &Router{routes: routes, cooldown: cooldown, coolUntil: map[string]time.Time{}, now: time.Now}
}

// Names lists the routes in order.
func (r *Router) Names() []string {
	out := make([]string, len(r.routes))
	for i, rt := range r.routes {
		out[i] = rt.Name
	}
	return out
}

func (r *Router) Complete(ctx context.Context, req Request) (Response, error) {
	// Healthy routes first (in order), then cooling ones as a last resort.
	r.mu.Lock()
	now := r.now()
	var healthy, cooling []Route
	for _, rt := range r.routes {
		if now.Before(r.coolUntil[rt.Name]) {
			cooling = append(cooling, rt)
		} else {
			healthy = append(healthy, rt)
		}
	}
	r.mu.Unlock()

	var lastErr error
	for _, rt := range append(healthy, cooling...) {
		resp, err := rt.Client.Complete(ctx, req)
		if err == nil {
			r.mu.Lock()
			delete(r.coolUntil, rt.Name)
			r.mu.Unlock()
			return resp, nil
		}
		if !Retryable(err) {
			return Response{}, err // a real error (bad request, auth): don't hide it
		}
		lastErr = err
		r.mu.Lock()
		r.coolUntil[rt.Name] = r.now().Add(r.cooldown)
		r.mu.Unlock()
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
	}
	return Response{}, fmt.Errorf("%w (%v)", ErrBusy, shortErr(lastErr))
}

func shortErr(err error) string {
	var api *APIError
	if errors.As(err, &api) {
		return fmt.Sprintf("last error: HTTP %d", api.Status)
	}
	if err == nil {
		return "no models configured"
	}
	return "last error: " + err.Error()
}
