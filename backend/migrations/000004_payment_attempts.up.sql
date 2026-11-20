-- Preserve multiple Razorpay payment attempts against one order/booking.
CREATE TABLE payment_attempts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    payment_id uuid NOT NULL REFERENCES payments ON DELETE CASCADE,
    provider_payment_id text NOT NULL UNIQUE,
    amount_minor bigint NOT NULL CHECK (amount_minor >= 0),
    currency char(3) NOT NULL,
    status text NOT NULL CHECK (status IN ('failed','captured','refunded')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE refunds ADD COLUMN provider_payment_id text;
ALTER TABLE refunds ADD COLUMN attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0);
ALTER TABLE refunds ADD COLUMN last_error text;
ALTER TABLE refunds ADD COLUMN next_attempt_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE refunds ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();
CREATE INDEX refunds_retry_idx ON refunds(status,next_attempt_at) WHERE status IN ('requires_refund','retry');
