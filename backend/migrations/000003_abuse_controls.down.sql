-- Rollback only when reverting this feature intentionally; both tables contain runtime counters.
DROP TABLE IF EXISTS login_throttles;
DROP TABLE IF EXISTS rate_limit_buckets;
DROP TABLE IF EXISTS webhook_events;
