package assistant

import (
	"context"
	"encoding/json"

	"github.com/sanjayrj/ai-bot/internal/plugin"
	"github.com/sanjayrj/ai-bot/internal/store"
)

// UserSettings gives plugins per-user preferences, scoped to the signed-in
// user taken from ctx.
type UserSettings struct{ Store store.Settings }

var _ plugin.Settings = UserSettings{}

func (s UserSettings) Load(ctx context.Context, key string, v any) (bool, error) {
	uid, err := userID(ctx)
	if err != nil {
		return false, err
	}
	b, err := s.Store.GetSetting(ctx, uid, key)
	if err != nil || b == nil {
		return false, err
	}
	return true, json.Unmarshal(b, v)
}

func (s UserSettings) Save(ctx context.Context, key string, v any) error {
	uid, err := userID(ctx)
	if err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.Store.PutSetting(ctx, uid, key, b)
}

type reasonKey struct{}

// withReason records why actions proposed during this request exist (the
// user's message, "Daily brief", …) for the activity log.
func withReason(ctx context.Context, reason string) context.Context {
	return context.WithValue(ctx, reasonKey{}, reason)
}

func reasonFrom(ctx context.Context) string {
	if r, ok := ctx.Value(reasonKey{}).(string); ok {
		return r
	}
	return "Requested through the tools API"
}
