package assistant

import (
	"context"
	"encoding/json"
	"time"

	"github.com/go-kit/kit/metrics"
	"github.com/go-kit/log"

	"github.com/sanjayrj/ai-bot/internal/store"
)

// Middleware decorates a Service.
type Middleware func(Service) Service

// LoggingMiddleware logs every service call with its duration and error.
func LoggingMiddleware(logger log.Logger) Middleware {
	return func(next Service) Service { return &loggingMiddleware{next: next, logger: logger} }
}

type loggingMiddleware struct {
	next   Service
	logger log.Logger
}

func (mw *loggingMiddleware) log(method string, begin time.Time, err error, kv ...any) {
	kv = append([]any{"method", method, "took", time.Since(begin), "err", err}, kv...)
	_ = mw.logger.Log(kv...)
}

func (mw *loggingMiddleware) Chat(ctx context.Context, sessionID, message string) (r ChatReply, err error) {
	defer func(b time.Time) { mw.log("Chat", b, err, "session", sessionID, "tools", len(r.ToolCalls)) }(time.Now())
	return mw.next.Chat(ctx, sessionID, message)
}

func (mw *loggingMiddleware) StartChat(ctx context.Context, sessionID, message string) (j Job, err error) {
	defer func(b time.Time) { mw.log("StartChat", b, err, "session", sessionID, "job", j.ID) }(time.Now())
	return mw.next.StartChat(ctx, sessionID, message)
}

func (mw *loggingMiddleware) GetJob(ctx context.Context, id string) (j Job, err error) {
	defer func(b time.Time) { mw.log("GetJob", b, err, "job", id, "status", j.Status) }(time.Now())
	return mw.next.GetJob(ctx, id)
}

func (mw *loggingMiddleware) CancelJob(ctx context.Context, id string) (j Job, err error) {
	defer func(b time.Time) { mw.log("CancelJob", b, err, "job", id) }(time.Now())
	return mw.next.CancelJob(ctx, id)
}

func (mw *loggingMiddleware) ConversationJob(ctx context.Context, conversationID string) (j *Job, err error) {
	defer func(b time.Time) {
		mw.log("ConversationJob", b, err, "conversation", conversationID, "found", j != nil)
	}(time.Now())
	return mw.next.ConversationJob(ctx, conversationID)
}

func (mw *loggingMiddleware) ListConversations(ctx context.Context) (c []store.Summary, err error) {
	defer func(b time.Time) { mw.log("ListConversations", b, err, "count", len(c)) }(time.Now())
	return mw.next.ListConversations(ctx)
}

func (mw *loggingMiddleware) GetConversation(ctx context.Context, id string) (c store.Conversation, err error) {
	defer func(b time.Time) { mw.log("GetConversation", b, err, "id", id, "turns", len(c.Turns)) }(time.Now())
	return mw.next.GetConversation(ctx, id)
}

func (mw *loggingMiddleware) DeleteConversation(ctx context.Context, id string) (err error) {
	defer func(b time.Time) { mw.log("DeleteConversation", b, err, "id", id) }(time.Now())
	return mw.next.DeleteConversation(ctx, id)
}

func (mw *loggingMiddleware) Briefing(ctx context.Context) (b Briefing, err error) {
	defer func(t time.Time) { mw.log("Briefing", t, err, "tools", len(b.ToolCalls)) }(time.Now())
	return mw.next.Briefing(ctx)
}

func (mw *loggingMiddleware) Overview(ctx context.Context) (o Overview, err error) {
	defer func(b time.Time) { mw.log("Overview", b, err, "partial_errors", len(o.Errors)) }(time.Now())
	return mw.next.Overview(ctx)
}

func (mw *loggingMiddleware) ListActions(ctx context.Context) (a []Action, err error) {
	defer func(b time.Time) { mw.log("ListActions", b, err, "count", len(a)) }(time.Now())
	return mw.next.ListActions(ctx)
}

func (mw *loggingMiddleware) ApproveAction(ctx context.Context, id string) (a Action, err error) {
	defer func(b time.Time) { mw.log("ApproveAction", b, err, "id", id, "kind", a.Kind, "status", a.Status) }(time.Now())
	return mw.next.ApproveAction(ctx, id)
}

func (mw *loggingMiddleware) RejectAction(ctx context.Context, id string) (a Action, err error) {
	defer func(b time.Time) { mw.log("RejectAction", b, err, "id", id) }(time.Now())
	return mw.next.RejectAction(ctx, id)
}

func (mw *loggingMiddleware) Status(ctx context.Context) (s Status, err error) {
	defer func(b time.Time) { mw.log("Status", b, err) }(time.Now())
	return mw.next.Status(ctx)
}

func (mw *loggingMiddleware) Tools(ctx context.Context) (t []ToolInfo, err error) {
	defer func(b time.Time) { mw.log("Tools", b, err, "count", len(t)) }(time.Now())
	return mw.next.Tools(ctx)
}

func (mw *loggingMiddleware) CallTool(ctx context.Context, name string, input json.RawMessage) (o ToolOutput, err error) {
	defer func(b time.Time) { mw.log("CallTool", b, err, "tool", name, "tool_error", o.Error) }(time.Now())
	return mw.next.CallTool(ctx, name, input)
}

// InstrumentingMiddleware records request counts, errors and latency per method.
func InstrumentingMiddleware(requests, errs metrics.Counter, latency metrics.Histogram) Middleware {
	return func(next Service) Service {
		return &instrumentingMiddleware{next: next, requests: requests, errs: errs, latency: latency}
	}
}

type instrumentingMiddleware struct {
	next     Service
	requests metrics.Counter
	errs     metrics.Counter
	latency  metrics.Histogram
}

func (mw *instrumentingMiddleware) observe(method string, begin time.Time, err error) {
	mw.requests.With("method", method).Add(1)
	if err != nil {
		mw.errs.With("method", method).Add(1)
	}
	mw.latency.With("method", method).Observe(time.Since(begin).Seconds())
}

func (mw *instrumentingMiddleware) Chat(ctx context.Context, sessionID, message string) (r ChatReply, err error) {
	defer func(b time.Time) { mw.observe("Chat", b, err) }(time.Now())
	return mw.next.Chat(ctx, sessionID, message)
}

func (mw *instrumentingMiddleware) StartChat(ctx context.Context, sessionID, message string) (j Job, err error) {
	defer func(b time.Time) { mw.observe("StartChat", b, err) }(time.Now())
	return mw.next.StartChat(ctx, sessionID, message)
}

func (mw *instrumentingMiddleware) GetJob(ctx context.Context, id string) (j Job, err error) {
	defer func(b time.Time) { mw.observe("GetJob", b, err) }(time.Now())
	return mw.next.GetJob(ctx, id)
}

func (mw *instrumentingMiddleware) CancelJob(ctx context.Context, id string) (j Job, err error) {
	defer func(b time.Time) { mw.observe("CancelJob", b, err) }(time.Now())
	return mw.next.CancelJob(ctx, id)
}

func (mw *instrumentingMiddleware) ConversationJob(ctx context.Context, conversationID string) (j *Job, err error) {
	defer func(b time.Time) { mw.observe("ConversationJob", b, err) }(time.Now())
	return mw.next.ConversationJob(ctx, conversationID)
}

func (mw *instrumentingMiddleware) ListConversations(ctx context.Context) (c []store.Summary, err error) {
	defer func(b time.Time) { mw.observe("ListConversations", b, err) }(time.Now())
	return mw.next.ListConversations(ctx)
}

func (mw *instrumentingMiddleware) GetConversation(ctx context.Context, id string) (c store.Conversation, err error) {
	defer func(b time.Time) { mw.observe("GetConversation", b, err) }(time.Now())
	return mw.next.GetConversation(ctx, id)
}

func (mw *instrumentingMiddleware) DeleteConversation(ctx context.Context, id string) (err error) {
	defer func(b time.Time) { mw.observe("DeleteConversation", b, err) }(time.Now())
	return mw.next.DeleteConversation(ctx, id)
}

func (mw *instrumentingMiddleware) Briefing(ctx context.Context) (b Briefing, err error) {
	defer func(t time.Time) { mw.observe("Briefing", t, err) }(time.Now())
	return mw.next.Briefing(ctx)
}

func (mw *instrumentingMiddleware) Overview(ctx context.Context) (o Overview, err error) {
	defer func(b time.Time) { mw.observe("Overview", b, err) }(time.Now())
	return mw.next.Overview(ctx)
}

func (mw *instrumentingMiddleware) ListActions(ctx context.Context) (a []Action, err error) {
	defer func(b time.Time) { mw.observe("ListActions", b, err) }(time.Now())
	return mw.next.ListActions(ctx)
}

func (mw *instrumentingMiddleware) ApproveAction(ctx context.Context, id string) (a Action, err error) {
	defer func(b time.Time) { mw.observe("ApproveAction", b, err) }(time.Now())
	return mw.next.ApproveAction(ctx, id)
}

func (mw *instrumentingMiddleware) RejectAction(ctx context.Context, id string) (a Action, err error) {
	defer func(b time.Time) { mw.observe("RejectAction", b, err) }(time.Now())
	return mw.next.RejectAction(ctx, id)
}

func (mw *instrumentingMiddleware) Status(ctx context.Context) (s Status, err error) {
	defer func(b time.Time) { mw.observe("Status", b, err) }(time.Now())
	return mw.next.Status(ctx)
}

func (mw *instrumentingMiddleware) Tools(ctx context.Context) (t []ToolInfo, err error) {
	defer func(b time.Time) { mw.observe("Tools", b, err) }(time.Now())
	return mw.next.Tools(ctx)
}

func (mw *instrumentingMiddleware) CallTool(ctx context.Context, name string, input json.RawMessage) (o ToolOutput, err error) {
	defer func(b time.Time) { mw.observe("CallTool", b, err) }(time.Now())
	return mw.next.CallTool(ctx, name, input)
}
