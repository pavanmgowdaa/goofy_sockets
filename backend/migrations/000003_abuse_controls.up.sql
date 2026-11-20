-- Additive, database-backed anti-abuse counters shared across API instances.
CREATE TABLE rate_limit_buckets (
    bucket_hash text PRIMARY KEY,
    window_started_at timestamptz NOT NULL,
    request_count integer NOT NULL CHECK (request_count >= 0),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX rate_limit_buckets_cleanup_idx ON rate_limit_buckets(updated_at);

CREATE TABLE login_throttles (
    identifier_hash text PRIMARY KEY,
    failed_attempts integer NOT NULL DEFAULT 0 CHECK (failed_attempts >= 0),
    window_started_at timestamptz NOT NULL,
    blocked_until timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX login_throttles_cleanup_idx ON login_throttles(updated_at);

CREATE TABLE webhook_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    provider text NOT NULL,
    provider_event_id text NOT NULL,
    event_type text NOT NULL,
    status text NOT NULL DEFAULT 'processing' CHECK (status IN ('processing','processed')),
    received_at timestamptz NOT NULL DEFAULT now(),
    processed_at timestamptz,
    UNIQUE(provider,provider_event_id)
);
