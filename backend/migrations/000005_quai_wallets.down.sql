-- Review wallet and transaction target data before rolling this migration back.
DROP INDEX IF EXISTS blockchain_transactions_queue_idx;
ALTER TABLE blockchain_transactions DROP COLUMN IF EXISTS target_address;
DROP INDEX IF EXISTS users_wallet_address_unique_idx;
ALTER TABLE users DROP COLUMN IF EXISTS wallet_address;
