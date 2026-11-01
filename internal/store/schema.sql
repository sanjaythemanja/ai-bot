-- Applied on startup; every statement is idempotent.

CREATE TABLE IF NOT EXISTS users (
    id            TEXT PRIMARY KEY,               -- "g:" + Google subject ID
    email         TEXT        NOT NULL UNIQUE,
    name          TEXT        NOT NULL DEFAULT '',
    google_token  BYTEA,                          -- AES-GCM sealed OAuth token
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_login_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Only the SHA-256 of the session cookie is stored.
CREATE TABLE IF NOT EXISTS sessions (
    token_hash BYTEA PRIMARY KEY,
    user_id    TEXT        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS sessions_user_idx ON sessions (user_id);

CREATE TABLE IF NOT EXISTS conversations (
    id         TEXT PRIMARY KEY,
    title      TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Added for multi-user; conversations from before sign-in have no owner and
-- are not shown to anyone.
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS user_id TEXT REFERENCES users (id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS conversations_user_idx ON conversations (user_id, updated_at DESC);

-- Model-facing history, replayed in order to resume a conversation.
CREATE TABLE IF NOT EXISTS messages (
    conversation_id TEXT        NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    seq             INT         NOT NULL,
    message         JSONB       NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (conversation_id, seq)
);

-- User-facing transcript shown in the UI.
CREATE TABLE IF NOT EXISTS turns (
    id              BIGSERIAL PRIMARY KEY,
    conversation_id TEXT        NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    user_text       TEXT        NOT NULL,
    reply           TEXT        NOT NULL DEFAULT '',
    tool_calls      JSONB,
    error           TEXT        NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS turns_conversation_idx ON turns (conversation_id, id);

-- Proposed writes (emails, event changes) awaiting or after approval.
CREATE TABLE IF NOT EXISTS actions (
    id         TEXT PRIMARY KEY,
    user_id    TEXT        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind       TEXT        NOT NULL,
    summary    TEXT        NOT NULL,
    payload    JSONB       NOT NULL,
    status     TEXT        NOT NULL,
    result     TEXT        NOT NULL DEFAULT '',
    error      TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS actions_user_idx ON actions (user_id, created_at DESC);
-- Why the action was proposed (the user's request), for the activity log.
ALTER TABLE actions ADD COLUMN IF NOT EXISTS reason TEXT NOT NULL DEFAULT '';

-- Small per-user preferences: protected time, sorting rules, learned writing style.
CREATE TABLE IF NOT EXISTS user_settings (
    user_id    TEXT        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    key        TEXT        NOT NULL,
    value      JSONB       NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, key)
);

-- Per-user links to other services (e.g. Slack), credential sealed with APP_SECRET.
CREATE TABLE IF NOT EXISTS connections (
    user_id    TEXT        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    provider   TEXT        NOT NULL,
    label      TEXT        NOT NULL DEFAULT '',
    secret     BYTEA       NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, provider)
);
