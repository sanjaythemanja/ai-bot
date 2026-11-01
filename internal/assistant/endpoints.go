package assistant

import (
	"context"
	"encoding/json"
	"time"

	"github.com/go-kit/kit/endpoint"
	"github.com/go-kit/log"

	"github.com/sanjayrj/ai-bot/internal/store"
)

// Endpoints collects every service method as a go-kit endpoint.
type Endpoints struct {
	Chat          endpoint.Endpoint
	StartChat     endpoint.Endpoint
	GetJob        endpoint.Endpoint
	CancelJob     endpoint.Endpoint
	ConvJob       endpoint.Endpoint
	ListConvs     endpoint.Endpoint
	GetConv       endpoint.Endpoint
	DeleteConv    endpoint.Endpoint
	Briefing      endpoint.Endpoint
	Overview      endpoint.Endpoint
	ListActions   endpoint.Endpoint
	ApproveAction endpoint.Endpoint
	RejectAction  endpoint.Endpoint
	Status        endpoint.Endpoint
	ListTools     endpoint.Endpoint
	CallTool      endpoint.Endpoint
}

// MakeEndpoints builds endpoints and wraps each in endpoint middleware.
func MakeEndpoints(s Service, logger log.Logger) Endpoints {
	wrap := func(name string, e endpoint.Endpoint) endpoint.Endpoint {
		return EndpointLogging(log.With(logger, "endpoint", name))(e)
	}
	return Endpoints{
		Chat:          wrap("chat", makeChatEndpoint(s)),
		StartChat:     wrap("start_chat", makeStartChatEndpoint(s)),
		GetJob:        wrap("get_job", makeGetJobEndpoint(s)),
		CancelJob:     wrap("cancel_job", makeCancelJobEndpoint(s)),
		ConvJob:       wrap("conversation_job", makeConvJobEndpoint(s)),
		ListConvs:     wrap("list_conversations", makeListConvsEndpoint(s)),
		GetConv:       wrap("get_conversation", makeGetConvEndpoint(s)),
		DeleteConv:    wrap("delete_conversation", makeDeleteConvEndpoint(s)),
		Briefing:      wrap("briefing", makeBriefingEndpoint(s)),
		Overview:      wrap("overview", makeOverviewEndpoint(s)),
		ListActions:   wrap("list_actions", makeListActionsEndpoint(s)),
		ApproveAction: wrap("approve_action", makeApproveEndpoint(s)),
		RejectAction:  wrap("reject_action", makeRejectEndpoint(s)),
		Status:        wrap("status", makeStatusEndpoint(s)),
		ListTools:     wrap("list_tools", makeListToolsEndpoint(s)),
		CallTool:      wrap("call_tool", makeCallToolEndpoint(s)),
	}
}

// EndpointLogging logs transport-level outcomes, including business errors
// carried inside responses (see Failer).
func EndpointLogging(logger log.Logger) endpoint.Middleware {
	return func(next endpoint.Endpoint) endpoint.Endpoint {
		return func(ctx context.Context, req any) (resp any, err error) {
			defer func(b time.Time) {
				var bizErr error
				if f, ok := resp.(Failer); ok {
					bizErr = f.Failed()
				}
				_ = logger.Log("transport_error", err, "business_error", bizErr, "took", time.Since(b))
			}(time.Now())
			return next(ctx, req)
		}
	}
}

// Failer is implemented by responses that carry a business-logic error, so
// the transport can encode it without treating it as a transport failure.
type Failer interface{ Failed() error }

type (
	chatRequest struct {
		SessionID string `json:"session_id"`
		Message   string `json:"message"`
	}
	chatResponse struct {
		ChatReply
		Err error `json:"-"`
	}
	jobRequest  struct{ ID string }
	jobResponse struct {
		Job *Job  `json:"job"` // null when a conversation has no job
		Err error `json:"-"`
	}
	convRequest       struct{ ID string }
	listConvsResponse struct {
		Conversations []store.Summary `json:"conversations"`
		Err           error           `json:"-"`
	}
	getConvResponse struct {
		store.Conversation
		Err error `json:"-"`
	}
	deleteConvResponse struct {
		OK  bool  `json:"ok"`
		Err error `json:"-"`
	}
	briefingResponse struct {
		Briefing
		Err error `json:"-"`
	}
	overviewResponse struct {
		Overview
		Err error `json:"-"`
	}
	listActionsResponse struct {
		Actions []Action `json:"actions"`
		Err     error    `json:"-"`
	}
	actionRequest  struct{ ID string }
	actionResponse struct {
		Action Action `json:"action"`
		Err    error  `json:"-"`
	}
	statusResponse struct {
		Status
		Err error `json:"-"`
	}
	listToolsResponse struct {
		Tools []ToolInfo `json:"tools"`
		Err   error      `json:"-"`
	}
	callToolRequest struct {
		Name  string
		Input json.RawMessage
	}
	callToolResponse struct {
		ToolOutput
		Err error `json:"-"`
	}
	emptyRequest struct{}
)

func (r chatResponse) Failed() error        { return r.Err }
func (r jobResponse) Failed() error         { return r.Err }
func (r listConvsResponse) Failed() error   { return r.Err }
func (r getConvResponse) Failed() error     { return r.Err }
func (r deleteConvResponse) Failed() error  { return r.Err }
func (r briefingResponse) Failed() error    { return r.Err }
func (r overviewResponse) Failed() error    { return r.Err }
func (r listActionsResponse) Failed() error { return r.Err }
func (r actionResponse) Failed() error      { return r.Err }
func (r statusResponse) Failed() error      { return r.Err }
func (r listToolsResponse) Failed() error   { return r.Err }
func (r callToolResponse) Failed() error    { return r.Err }

func makeChatEndpoint(s Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(chatRequest)
		r, err := s.Chat(ctx, req.SessionID, req.Message)
		return chatResponse{ChatReply: r, Err: err}, nil
	}
}

func makeStartChatEndpoint(s Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(chatRequest)
		j, err := s.StartChat(ctx, req.SessionID, req.Message)
		return jobResponse{Job: &j, Err: err}, nil
	}
}

func makeGetJobEndpoint(s Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		j, err := s.GetJob(ctx, request.(jobRequest).ID)
		return jobResponse{Job: &j, Err: err}, nil
	}
}

func makeCancelJobEndpoint(s Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		j, err := s.CancelJob(ctx, request.(jobRequest).ID)
		return jobResponse{Job: &j, Err: err}, nil
	}
}

func makeConvJobEndpoint(s Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		j, err := s.ConversationJob(ctx, request.(convRequest).ID)
		return jobResponse{Job: j, Err: err}, nil
	}
}

func makeListConvsEndpoint(s Service) endpoint.Endpoint {
	return func(ctx context.Context, _ any) (any, error) {
		c, err := s.ListConversations(ctx)
		return listConvsResponse{Conversations: c, Err: err}, nil
	}
}

func makeGetConvEndpoint(s Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		c, err := s.GetConversation(ctx, request.(convRequest).ID)
		return getConvResponse{Conversation: c, Err: err}, nil
	}
}

func makeDeleteConvEndpoint(s Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		err := s.DeleteConversation(ctx, request.(convRequest).ID)
		return deleteConvResponse{OK: err == nil, Err: err}, nil
	}
}

func makeBriefingEndpoint(s Service) endpoint.Endpoint {
	return func(ctx context.Context, _ any) (any, error) {
		b, err := s.Briefing(ctx)
		return briefingResponse{Briefing: b, Err: err}, nil
	}
}

func makeOverviewEndpoint(s Service) endpoint.Endpoint {
	return func(ctx context.Context, _ any) (any, error) {
		o, err := s.Overview(ctx)
		return overviewResponse{Overview: o, Err: err}, nil
	}
}

func makeListActionsEndpoint(s Service) endpoint.Endpoint {
	return func(ctx context.Context, _ any) (any, error) {
		a, err := s.ListActions(ctx)
		return listActionsResponse{Actions: a, Err: err}, nil
	}
}

func makeApproveEndpoint(s Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		a, err := s.ApproveAction(ctx, request.(actionRequest).ID)
		return actionResponse{Action: a, Err: err}, nil
	}
}

func makeRejectEndpoint(s Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		a, err := s.RejectAction(ctx, request.(actionRequest).ID)
		return actionResponse{Action: a, Err: err}, nil
	}
}

func makeStatusEndpoint(s Service) endpoint.Endpoint {
	return func(ctx context.Context, _ any) (any, error) {
		st, err := s.Status(ctx)
		return statusResponse{Status: st, Err: err}, nil
	}
}

func makeListToolsEndpoint(s Service) endpoint.Endpoint {
	return func(ctx context.Context, _ any) (any, error) {
		t, err := s.Tools(ctx)
		return listToolsResponse{Tools: t, Err: err}, nil
	}
}

func makeCallToolEndpoint(s Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(callToolRequest)
		out, err := s.CallTool(ctx, req.Name, req.Input)
		return callToolResponse{ToolOutput: out, Err: err}, nil
	}
}
