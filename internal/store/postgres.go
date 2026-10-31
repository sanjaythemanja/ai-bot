package store

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sanjayrj/ai-bot/internal/llm"
)

//go:embed schema.sql
var schema string

// Postgres stores users, sessions, conversations and actions in PostgreSQL.
// It implements Store and Users; Actions() returns the Actions view.
type Postgres struct{ pool *pgxpool.Pool }

// OpenPostgres connects, waits briefly for the database, and applies the schema.
func OpenPostgres(ctx context.Context, url string) (*Postgres, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("postgres config: %w", err)
	}
	var pingErr error
	for i := 0; i < 10; i++ { // the container may still be starting
		if pingErr = pool.Ping(ctx); pingErr == nil {
			break
		}
		select {
		case <-ctx.Done():
			pool.Close()
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	if pingErr != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres unreachable (is `make db-up` running?): %w", pingErr)
	}
	if _, err := pool.Exec(ctx, schema); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres schema: %w", err)
	}
	return &Postgres{pool: pool}, nil
}

func (p *Postgres) Close() { p.pool.Close() }

// ---------- conversations ----------

func (p *Postgres) List(ctx context.Context, userID string, limit int) ([]Summary, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := p.pool.Query(ctx, `
		SELECT c.id, c.title, c.created_at, c.updated_at,
		       (SELECT count(*) FROM turns t WHERE t.conversation_id = c.id)
		FROM conversations c WHERE c.user_id = $1
		ORDER BY c.updated_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Summary, error) {
		var s Summary
		err := r.Scan(&s.ID, &s.Title, &s.CreatedAt, &s.UpdatedAt, &s.Turns)
		return s, err
	})
}

func (p *Postgres) Get(ctx context.Context, userID, id string) (Conversation, error) {
	var c Conversation
	err := p.pool.QueryRow(ctx, `SELECT id, title, created_at, updated_at FROM conversations WHERE id = $1 AND user_id = $2`, id, userID).
		Scan(&c.ID, &c.Title, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	if err != nil {
		return c, err
	}
	rows, err := p.pool.Query(ctx, `
		SELECT user_text, reply, tool_calls, error, created_at
		FROM turns WHERE conversation_id = $1 ORDER BY id`, id)
	if err != nil {
		return c, err
	}
	c.Turns, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Turn, error) {
		var t Turn
		var calls []byte
		err := r.Scan(&t.UserText, &t.Reply, &calls, &t.Error, &t.CreatedAt)
		t.ToolCalls = calls
		return t, err
	})
	c.Summary.Turns = len(c.Turns)
	return c, err
}

func (p *Postgres) History(ctx context.Context, userID, id string) ([]llm.Message, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT m.message FROM messages m
		JOIN conversations c ON c.id = m.conversation_id
		WHERE m.conversation_id = $1 AND c.user_id = $2 ORDER BY m.seq`, id, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (llm.Message, error) {
		var raw []byte
		var m llm.Message
		if err := r.Scan(&raw); err != nil {
			return m, err
		}
		return m, json.Unmarshal(raw, &m)
	})
}

func (p *Postgres) AppendTurn(ctx context.Context, userID, id string, msgs []llm.Message, turn Turn) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit

	// Create, or touch if it's ours. If the ID belongs to another user the
	// WHERE fails, no row comes back, and we refuse.
	var owner string
	err = tx.QueryRow(ctx, `
		INSERT INTO conversations (id, title, user_id) VALUES ($1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET updated_at = now() WHERE conversations.user_id = EXCLUDED.user_id
		RETURNING user_id`, id, TitleFrom(turn.UserText), userID).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	var next int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(max(seq) + 1, 0) FROM messages WHERE conversation_id = $1`, id).Scan(&next); err != nil {
		return err
	}
	for i, m := range msgs {
		b, err := json.Marshal(m)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO messages (conversation_id, seq, message) VALUES ($1, $2, $3)`, id, next+i, b); err != nil {
			return err
		}
	}
	var calls any
	if len(turn.ToolCalls) > 0 {
		calls = []byte(turn.ToolCalls)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO turns (conversation_id, user_text, reply, tool_calls, error) VALUES ($1, $2, $3, $4, $5)`,
		id, turn.UserText, turn.Reply, calls, turn.Error); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) Delete(ctx context.Context, userID, id string) error {
	tag, err := p.pool.Exec(ctx, `DELETE FROM conversations WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------- users & sessions ----------

const userCols = `id, email, name, created_at, last_login_at`

func scanUser(r pgx.Row) (User, error) {
	var u User
	err := r.Scan(&u.ID, &u.Email, &u.Name, &u.CreatedAt, &u.LastLoginAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return u, ErrNoSession
	}
	return u, err
}

func (p *Postgres) UpsertUser(ctx context.Context, id, email, name string) (User, error) {
	return scanUser(p.pool.QueryRow(ctx, `
		INSERT INTO users (id, email, name) VALUES ($1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET email = EXCLUDED.email, name = EXCLUDED.name, last_login_at = now()
		RETURNING `+userCols, id, strings.ToLower(email), name))
}

func (p *Postgres) GetUser(ctx context.Context, id string) (User, error) {
	return scanUser(p.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, id))
}

func (p *Postgres) UserByEmail(ctx context.Context, email string) (User, error) {
	return scanUser(p.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE email = $1`, strings.ToLower(email)))
}

func (p *Postgres) GoogleToken(ctx context.Context, userID string) ([]byte, error) {
	var b []byte
	err := p.pool.QueryRow(ctx, `SELECT google_token FROM users WHERE id = $1`, userID).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && b == nil) {
		return nil, ErrNoSession
	}
	return b, err
}

func (p *Postgres) SaveGoogleToken(ctx context.Context, userID string, sealed []byte) error {
	_, err := p.pool.Exec(ctx, `UPDATE users SET google_token = $2 WHERE id = $1`, userID, sealed)
	return err
}

func (p *Postgres) CreateSession(ctx context.Context, userID string, tokenHash []byte, expires time.Time) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`, tokenHash, userID, expires)
	return err
}

func (p *Postgres) SessionUser(ctx context.Context, tokenHash []byte) (User, error) {
	return scanUser(p.pool.QueryRow(ctx, `
		SELECT u.id, u.email, u.name, u.created_at, u.last_login_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > now()`, tokenHash))
}

func (p *Postgres) DeleteSession(ctx context.Context, tokenHash []byte) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1 OR expires_at < now()`, tokenHash)
	return err
}

// ---------- actions ----------

// PGActions is the Actions view of Postgres.
type PGActions struct{ *Postgres }

// Actions returns the Actions view backed by the same pool.
func (p *Postgres) Actions() PGActions { return PGActions{p} }

const actionCols = `id, user_id, kind, summary, payload, status, result, error, created_at, updated_at`

func scanAction(r pgx.Row) (Action, error) {
	var a Action
	var payload []byte
	err := r.Scan(&a.ID, &a.UserID, &a.Kind, &a.Summary, &payload, &a.Status, &a.Result, &a.Error, &a.CreatedAt, &a.UpdatedAt)
	a.Payload = payload
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ErrActionNotFound
	}
	return a, err
}

func (v PGActions) Add(ctx context.Context, userID, kind, summary string, payload any) (Action, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return Action{}, err
	}
	return scanAction(v.pool.QueryRow(ctx, `
		INSERT INTO actions (id, user_id, kind, summary, payload, status) VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+actionCols, NewID(), userID, kind, summary, b, StatusPending))
}

func (v PGActions) Get(ctx context.Context, userID, id string) (Action, error) {
	return scanAction(v.pool.QueryRow(ctx, `SELECT `+actionCols+` FROM actions WHERE id = $1 AND user_id = $2`, id, userID))
}

func (v PGActions) List(ctx context.Context, userID string, limit int) ([]Action, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := v.pool.Query(ctx, `SELECT `+actionCols+` FROM actions WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Action, error) { return scanAction(r) })
}

// resolved explains why a conditional update matched no row.
func (v PGActions) resolved(ctx context.Context, userID, id string) (Action, error) {
	a, err := v.Get(ctx, userID, id)
	if err != nil {
		return a, err
	}
	return a, ErrActionResolved
}

func (v PGActions) Claim(ctx context.Context, userID, id, status string) (Action, error) {
	a, err := scanAction(v.pool.QueryRow(ctx, `
		UPDATE actions SET status = $3, updated_at = now()
		WHERE id = $1 AND user_id = $2 AND status = 'pending'
		RETURNING `+actionCols, id, userID, status))
	if errors.Is(err, ErrActionNotFound) {
		return v.resolved(ctx, userID, id)
	}
	return a, err
}

func (v PGActions) UpdatePending(ctx context.Context, userID, id, summary string, payload any) (Action, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return Action{}, err
	}
	a, err := scanAction(v.pool.QueryRow(ctx, `
		UPDATE actions SET summary = $3, payload = $4, updated_at = now()
		WHERE id = $1 AND user_id = $2 AND status = 'pending'
		RETURNING `+actionCols, id, userID, summary, b))
	if errors.Is(err, ErrActionNotFound) {
		return v.resolved(ctx, userID, id)
	}
	return a, err
}

func (v PGActions) Finish(ctx context.Context, userID, id, status, result, errMsg string) (Action, error) {
	return scanAction(v.pool.QueryRow(ctx, `
		UPDATE actions SET status = $3, result = $4, error = $5, updated_at = now()
		WHERE id = $1 AND user_id = $2
		RETURNING `+actionCols, id, userID, status, result, errMsg))
}

var (
	_ Store   = (*Postgres)(nil)
	_ Users   = (*Postgres)(nil)
	_ Actions = PGActions{}
)
