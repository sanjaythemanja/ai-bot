// Package slack implements provider.Chat on the Slack Web API, acting as the
// signed-in user with that user's own token (from their Slack connection).
package slack

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/slack-go/slack"

	"github.com/sanjayrj/ai-bot/internal/provider"
	"github.com/sanjayrj/ai-bot/internal/store"
)

// Provider is the connection name used for Slack credentials.
const Provider = "slack"

// TokenLoader returns the signed-in user's Slack token.
type TokenLoader interface {
	Load(ctx context.Context, provider string) (string, error)
}

// Client implements provider.Chat for whichever user is in the context.
type Client struct {
	tokens TokenLoader

	mu    sync.Mutex
	names map[string]string // user ID -> display name (shared, IDs are global)
}

func New(tokens TokenLoader) *Client { return &Client{tokens: tokens, names: map[string]string{}} }

var _ provider.Chat = (*Client)(nil)

func (c *Client) api(ctx context.Context) (*slack.Client, error) {
	tok, err := c.tokens.Load(ctx, Provider)
	if errors.Is(err, store.ErrNotConnected) {
		return nil, fmt.Errorf("%w: Slack isn't connected; the user can connect it from the sidebar", provider.ErrNotConnected)
	}
	if err != nil {
		return nil, err
	}
	return slack.New(tok), nil
}

func (c *Client) ListChannels(ctx context.Context) ([]provider.Channel, error) {
	api, err := c.api(ctx)
	if err != nil {
		return nil, err
	}
	var out []provider.Channel
	params := &slack.GetConversationsForUserParameters{
		Types: []string{"public_channel", "private_channel", "im", "mpim"}, ExcludeArchived: true, Limit: 200,
	}
	for {
		chs, cursor, err := api.GetConversationsForUserContext(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("slack conversations: %w", err)
		}
		for _, ch := range chs {
			name := ch.Name
			if ch.IsIM {
				name = "dm:" + c.userName(ctx, api, ch.User)
			}
			out = append(out, provider.Channel{ID: ch.ID, Name: name, IsPrivate: ch.IsPrivate, IsIM: ch.IsIM})
		}
		if cursor == "" || len(out) >= 1000 {
			break
		}
		params.Cursor = cursor
	}
	return out, nil
}

func (c *Client) History(ctx context.Context, channel string, limit int) ([]provider.ChatMessage, error) {
	api, err := c.api(ctx)
	if err != nil {
		return nil, err
	}
	id, name, err := c.resolve(ctx, channel)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	res, err := api.GetConversationHistoryContext(ctx, &slack.GetConversationHistoryParameters{ChannelID: id, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("slack history: %w", err)
	}
	out := make([]provider.ChatMessage, 0, len(res.Messages))
	for _, m := range res.Messages {
		out = append(out, provider.ChatMessage{
			Channel: id, ChannelName: name, User: c.userName(ctx, api, m.User),
			Text: m.Text, TS: m.Timestamp, ThreadTS: m.ThreadTimestamp, Time: tsTime(m.Timestamp),
		})
	}
	return out, nil
}

func (c *Client) Search(ctx context.Context, query string, limit int) ([]provider.ChatMessage, error) {
	api, err := c.api(ctx)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	res, err := api.SearchMessagesContext(ctx, query, slack.SearchParameters{Sort: "timestamp", SortDirection: "desc", Count: limit, Page: 1})
	if err != nil {
		return nil, fmt.Errorf("slack search: %w", err)
	}
	out := make([]provider.ChatMessage, 0, len(res.Matches))
	for _, m := range res.Matches {
		user := m.Username
		if user == "" {
			user = c.userName(ctx, api, m.User)
		}
		out = append(out, provider.ChatMessage{
			Channel: m.Channel.ID, ChannelName: m.Channel.Name, User: user,
			Text: m.Text, TS: m.Timestamp, Time: tsTime(m.Timestamp), Permalink: m.Permalink,
		})
	}
	return out, nil
}

func (c *Client) Mentions(ctx context.Context, limit int) ([]provider.ChatMessage, error) {
	api, err := c.api(ctx)
	if err != nil {
		return nil, err
	}
	me, err := api.AuthTestContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("slack auth test: %w", err)
	}
	after := time.Now().AddDate(0, 0, -3).Format("2006-01-02")
	return c.Search(ctx, fmt.Sprintf("<@%s> after:%s", me.UserID, after), limit)
}

func (c *Client) Post(ctx context.Context, channel, text, threadTS string) (string, error) {
	api, err := c.api(ctx)
	if err != nil {
		return "", err
	}
	id, _, err := c.resolve(ctx, channel)
	if err != nil {
		return "", err
	}
	opts := []slack.MsgOption{slack.MsgOptionText(text, false)}
	if threadTS != "" {
		opts = append(opts, slack.MsgOptionTS(threadTS))
	}
	_, ts, err := api.PostMessageContext(ctx, id, opts...)
	if err != nil {
		return "", fmt.Errorf("slack post: %w", err)
	}
	return ts, nil
}

// resolve accepts a channel ID, "#name" or "name" and returns ID and name.
func (c *Client) resolve(ctx context.Context, channel string) (string, string, error) {
	channel = strings.TrimPrefix(strings.TrimSpace(channel), "#")
	if channel == "" {
		return "", "", errors.New("channel is required")
	}
	chs, err := c.ListChannels(ctx)
	if err != nil {
		return "", "", err
	}
	for _, ch := range chs {
		if ch.ID == channel || strings.EqualFold(ch.Name, channel) {
			return ch.ID, ch.Name, nil
		}
	}
	return "", "", fmt.Errorf("no channel %q among the user's channels; use list_slack_channels", channel)
}

func (c *Client) userName(ctx context.Context, api *slack.Client, id string) string {
	if id == "" {
		return ""
	}
	c.mu.Lock()
	name, ok := c.names[id]
	c.mu.Unlock()
	if ok {
		return name
	}
	name = id
	if u, err := api.GetUserInfoContext(ctx, id); err == nil {
		name = u.RealName
		if u.Profile.DisplayName != "" {
			name = u.Profile.DisplayName
		}
	}
	c.mu.Lock()
	c.names[id] = name
	c.mu.Unlock()
	return name
}

func tsTime(ts string) time.Time {
	f, err := strconv.ParseFloat(ts, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(int64(f), 0)
}
