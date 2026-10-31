package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-kit/kit/transport"
	httptransport "github.com/go-kit/kit/transport/http"
	"github.com/go-kit/log"

	"github.com/sanjayrj/ai-bot/internal/auth"
	"github.com/sanjayrj/ai-bot/internal/llm"
	"github.com/sanjayrj/ai-bot/internal/store"
)

var errBadRequest = errors.New("invalid request body")

// RegisterHTTPRoutes mounts the JSON API under /api on mux.
func RegisterHTTPRoutes(mux *http.ServeMux, e Endpoints, logger log.Logger) {
	opts := []httptransport.ServerOption{
		httptransport.ServerErrorHandler(transport.NewLogErrorHandler(logger)),
		httptransport.ServerErrorEncoder(encodeError),
	}
	handle := func(pattern string, ep func(context.Context, any) (any, error), dec httptransport.DecodeRequestFunc) {
		mux.Handle(pattern, httptransport.NewServer(ep, dec, encodeResponse, opts...))
	}

	handle("POST /api/chat", e.Chat, decodeChat)
	handle("GET /api/conversations", e.ListConvs, decodeEmpty)
	handle("GET /api/conversations/{id}", e.GetConv, decodeConv)
	handle("POST /api/conversations/{id}/delete", e.DeleteConv, decodeConv)
	handle("POST /api/briefing", e.Briefing, decodeEmpty)
	handle("GET /api/overview", e.Overview, decodeEmpty)
	handle("GET /api/actions", e.ListActions, decodeEmpty)
	handle("POST /api/actions/{id}/approve", e.ApproveAction, decodeAction)
	handle("POST /api/actions/{id}/reject", e.RejectAction, decodeAction)
	handle("GET /api/status", e.Status, decodeEmpty)

	// Agent-facing tool API: any external agent can list and call the tools.
	handle("GET /api/tools", e.ListTools, decodeEmpty)
	handle("POST /api/tools/{name}", e.CallTool, decodeCallTool)
}

func decodeCallTool(_ context.Context, r *http.Request) (any, error) {
	var input json.RawMessage
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 256<<10)).Decode(&input); err != nil {
		return nil, errBadRequest
	}
	return callToolRequest{Name: r.PathValue("name"), Input: input}, nil
}

func decodeChat(_ context.Context, r *http.Request) (any, error) {
	var req chatRequest
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 64<<10)).Decode(&req); err != nil {
		return nil, errBadRequest
	}
	return req, nil
}

func decodeConv(_ context.Context, r *http.Request) (any, error) {
	return convRequest{ID: r.PathValue("id")}, nil
}

func decodeAction(_ context.Context, r *http.Request) (any, error) {
	return actionRequest{ID: r.PathValue("id")}, nil
}

func decodeEmpty(context.Context, *http.Request) (any, error) { return emptyRequest{}, nil }

func encodeResponse(ctx context.Context, w http.ResponseWriter, response any) error {
	if f, ok := response.(Failer); ok && f.Failed() != nil {
		encodeError(ctx, f.Failed(), w)
		return nil
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	return json.NewEncoder(w).Encode(response)
}

func encodeError(_ context.Context, err error, w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(codeFrom(err))
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func codeFrom(err error) int {
	switch {
	case errors.Is(err, errBadRequest), errors.Is(err, ErrEmptyMessage):
		return http.StatusBadRequest
	case errors.Is(err, ErrActionNotFound), errors.Is(err, ErrUnknownTool), errors.Is(err, store.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrActionResolved):
		return http.StatusConflict
	case errors.Is(err, auth.ErrUnauthorized):
		return http.StatusUnauthorized
	case errors.Is(err, llm.ErrBusy):
		return http.StatusServiceUnavailable
	case errors.Is(err, llm.ErrNotConfigured):
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}
