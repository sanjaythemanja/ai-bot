package store

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sanjayrj/ai-bot/internal/llm"
)

// Memory keeps everything in process memory (lost on restart). Used when no
// DATABASE_URL is set, in demo mode, and in tests. It implements Store,
// Users and Actions.
type Memory struct {
	mu       sync.Mutex
	convs    map[string]*memConv
	users    map[string]*memUser
	sessions map[string]memSession // key: hex of token hash
	actions  map[string]*Action
	conns    map[string]memConn // key: userID + "/" + provider
	settings map[string][]byte  // key: userID + "/" + key
}

type memConn struct {
	Connection
	userID string
	sealed []byte
}

type memConv struct {
	userID string
	sum    Summary
	msgs   []llm.Message
	turns  []Turn
}

type memUser struct {
	User
	token []byte
}

type memSession struct {
	userID  string
	expires time.Time
}

func NewMemory() *Memory {
	return &Memory{
		convs:    map[string]*memConv{},
		users:    map[string]*memUser{},
		sessions: map[string]memSession{},
		actions:  map[string]*Action{},
		conns:    map[string]memConn{},
		settings: map[string][]byte{},
	}
}

func (m *Memory) Close() {}

// ---------- conversations ----------

func (m *Memory) conv(userID, id string) (*memConv, bool) {
	c, ok := m.convs[id]
	if !ok || c.userID != userID {
		return nil, false
	}
	return c, true
}

func (m *Memory) List(_ context.Context, userID string, limit int) ([]Summary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Summary{}
	for _, c := range m.convs {
		if c.userID == userID {
			out = append(out, c.sum)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *Memory) Get(_ context.Context, userID, id string) (Conversation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.conv(userID, id)
	if !ok {
		return Conversation{}, ErrNotFound
	}
	return Conversation{Summary: c.sum, Turns: append([]Turn(nil), c.turns...)}, nil
}

func (m *Memory) History(_ context.Context, userID, id string) ([]llm.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.conv(userID, id); ok {
		return append([]llm.Message(nil), c.msgs...), nil
	}
	return nil, nil
}

func (m *Memory) AppendTurn(_ context.Context, userID, id string, msgs []llm.Message, turn Turn) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	c, exists := m.convs[id]
	if exists && c.userID != userID {
		return ErrNotFound // ID belongs to someone else
	}
	if !exists {
		c = &memConv{userID: userID, sum: Summary{ID: id, Title: TitleFrom(turn.UserText), CreatedAt: now}}
		m.convs[id] = c
	}
	if turn.CreatedAt.IsZero() {
		turn.CreatedAt = now
	}
	c.msgs = append(c.msgs, msgs...)
	c.turns = append(c.turns, turn)
	c.sum.Turns, c.sum.UpdatedAt = len(c.turns), now
	return nil
}

func (m *Memory) Delete(_ context.Context, userID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.conv(userID, id); !ok {
		return ErrNotFound
	}
	delete(m.convs, id)
	return nil
}

// ---------- users & sessions ----------

func (m *Memory) UpsertUser(_ context.Context, id, email, name string) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	u, ok := m.users[id]
	if !ok {
		u = &memUser{User: User{ID: id, CreatedAt: now}}
		m.users[id] = u
	}
	u.Email, u.Name, u.LastLoginAt = strings.ToLower(email), name, now
	return u.User, nil
}

func (m *Memory) GetUser(_ context.Context, id string) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u, ok := m.users[id]; ok {
		return u.User, nil
	}
	return User{}, ErrNoSession
}

func (m *Memory) UserByEmail(_ context.Context, email string) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if strings.EqualFold(u.Email, email) {
			return u.User, nil
		}
	}
	return User{}, ErrNoSession
}

func (m *Memory) GoogleToken(_ context.Context, userID string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u, ok := m.users[userID]; ok && u.token != nil {
		return append([]byte(nil), u.token...), nil
	}
	return nil, ErrNoSession
}

func (m *Memory) SaveGoogleToken(_ context.Context, userID string, sealed []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[userID]
	if !ok {
		return ErrNoSession
	}
	u.token = append([]byte(nil), sealed...)
	return nil
}

func (m *Memory) CreateSession(_ context.Context, userID string, tokenHash []byte, expires time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[string(tokenHash)] = memSession{userID: userID, expires: expires}
	return nil
}

func (m *Memory) SessionUser(_ context.Context, tokenHash []byte) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[string(tokenHash)]
	if !ok || time.Now().After(s.expires) {
		delete(m.sessions, string(tokenHash))
		return User{}, ErrNoSession
	}
	u, ok := m.users[s.userID]
	if !ok {
		return User{}, ErrNoSession
	}
	return u.User, nil
}

func (m *Memory) DeleteSession(_ context.Context, tokenHash []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, string(tokenHash))
	return nil
}

// ---------- actions ----------

func (m *Memory) action(userID, id string) (*Action, error) {
	a, ok := m.actions[id]
	if !ok || a.UserID != userID {
		return nil, ErrActionNotFound
	}
	return a, nil
}

func (m *Memory) Add(_ context.Context, userID, kind, summary, reason string, payload any) (Action, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return Action{}, err
	}
	now := time.Now()
	a := &Action{ID: NewID(), UserID: userID, Kind: kind, Summary: summary, Reason: reason, Payload: b, Status: StatusPending, CreatedAt: now, UpdatedAt: now}
	m.mu.Lock()
	m.actions[a.ID] = a
	m.mu.Unlock()
	return *a, nil
}

func (m *Memory) getAction(userID, id string) (Action, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.action(userID, id)
	if err != nil {
		return Action{}, err
	}
	return *a, nil
}

func (m *Memory) listActions(userID string, limit int) []Action {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Action{}
	for _, a := range m.actions {
		if a.UserID == userID {
			out = append(out, *a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (m *Memory) Claim(_ context.Context, userID, id, status string) (Action, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.action(userID, id)
	if err != nil {
		return Action{}, err
	}
	if a.Status != StatusPending {
		return *a, ErrActionResolved
	}
	a.Status, a.UpdatedAt = status, time.Now()
	return *a, nil
}

func (m *Memory) UpdatePending(_ context.Context, userID, id, summary string, payload any) (Action, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return Action{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.action(userID, id)
	if err != nil {
		return Action{}, err
	}
	if a.Status != StatusPending {
		return *a, ErrActionResolved
	}
	a.Summary, a.Payload, a.UpdatedAt = summary, b, time.Now()
	return *a, nil
}

func (m *Memory) Finish(_ context.Context, userID, id, status, result, errMsg string) (Action, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.action(userID, id)
	if err != nil {
		return Action{}, err
	}
	a.Status, a.Result, a.Error, a.UpdatedAt = status, result, errMsg, time.Now()
	return *a, nil
}

// ---------- connections ----------

func (m *Memory) SaveConnection(_ context.Context, userID, provider, label string, sealed []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.conns[userID+"/"+provider] = memConn{
		Connection: Connection{Provider: provider, Label: label, CreatedAt: time.Now()},
		userID:     userID, sealed: append([]byte(nil), sealed...),
	}
	return nil
}

func (m *Memory) ConnectionSecret(_ context.Context, userID, provider string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.conns[userID+"/"+provider]
	if !ok {
		return nil, ErrNotConnected
	}
	return append([]byte(nil), c.sealed...), nil
}

func (m *Memory) ListConnections(_ context.Context, userID string) ([]Connection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Connection{}
	for _, c := range m.conns {
		if c.userID == userID {
			out = append(out, c.Connection)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Provider < out[j].Provider })
	return out, nil
}

func (m *Memory) DeleteConnection(_ context.Context, userID, provider string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.conns, userID+"/"+provider)
	return nil
}

// ---------- settings ----------

func (m *Memory) GetSetting(_ context.Context, userID, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if v, ok := m.settings[userID+"/"+key]; ok {
		return append([]byte(nil), v...), nil
	}
	return nil, nil
}

func (m *Memory) PutSetting(_ context.Context, userID, key string, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings[userID+"/"+key] = append([]byte(nil), value...)
	return nil
}

// ActionsView adapts Memory to the Actions interface (Memory's own Get and
// List are the conversation methods).
type ActionsView struct{ *Memory }

func (v ActionsView) Get(_ context.Context, userID, id string) (Action, error) {
	return v.getAction(userID, id)
}

func (v ActionsView) List(_ context.Context, userID string, limit int) ([]Action, error) {
	return v.listActions(userID, limit), nil
}

var (
	_ Store       = (*Memory)(nil)
	_ Users       = (*Memory)(nil)
	_ Connections = (*Memory)(nil)
	_ Settings    = (*Memory)(nil)
	_ Actions     = ActionsView{}
)
