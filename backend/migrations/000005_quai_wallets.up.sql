-- Add public wallet references and durable target data for Quai transaction jobs.
ALTER TABLE users ADD COLUMN wallet_address text;
CREATE UNIQUE INDEX users_wallet_address_unique_idx ON users(lower(wallet_address)) WHERE wallet_address IS NOT NULL;
ALTER TABLE blockchain_transactions ADD COLUMN target_address text;
CREATE INDEX blockchain_transactions_queue_idx ON blockchain_transactions(status,created_at) WHERE status IN ('queued','submitted');
