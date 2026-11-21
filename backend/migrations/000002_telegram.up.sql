CREATE TABLE IF NOT EXISTS telegram_links (
    user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    chat_id bigint NOT NULL UNIQUE,
    username text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS telegram_link_tokens (
    token text PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_telegram_link_tokens_user ON telegram_link_tokens(user_id);
