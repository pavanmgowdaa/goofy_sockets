-- This rollback removes payment attempt and retry history. Review retained data before use.
DROP INDEX IF EXISTS refunds_retry_idx;
ALTER TABLE refunds DROP COLUMN IF EXISTS updated_at;
ALTER TABLE refunds DROP COLUMN IF EXISTS next_attempt_at;
ALTER TABLE refunds DROP COLUMN IF EXISTS last_error;
ALTER TABLE refunds DROP COLUMN IF EXISTS attempts;
ALTER TABLE refunds DROP COLUMN IF EXISTS provider_payment_id;
DROP TABLE IF EXISTS payment_attempts;
