-- +goose Up
-- Harden: bounded refresh-token chain (depth 10 uses expires_at / superseded_by) and cap pagination
CREATE INDEX IF NOT EXISTS refresh_tokens_expires_at_idx ON refresh_tokens(expires_at);
CREATE INDEX IF NOT EXISTS refresh_tokens_superseded_by_idx ON refresh_tokens(superseded_by) WHERE superseded_by IS NOT NULL;
-- Ensure users / hospitals / wallet pagination is index-backed
CREATE INDEX IF NOT EXISTS users_created_at_id_idx ON users(created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS unverified_drivers_created_at_id_idx ON unverified_drivers(created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS hospitals_created_at_id_idx ON hospitals(created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS offers_created_at_id_idx ON offers(created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS wallet_transactions_driver_created_at_id_idx ON wallet_transactions(driver_id, created_at DESC, id DESC);

-- +goose Down
DROP INDEX IF EXISTS wallet_transactions_driver_created_at_id_idx;
DROP INDEX IF EXISTS offers_created_at_id_idx;
DROP INDEX IF EXISTS hospitals_created_at_id_idx;
DROP INDEX IF EXISTS unverified_drivers_created_at_id_idx;
DROP INDEX IF EXISTS users_created_at_id_idx;
DROP INDEX IF EXISTS refresh_tokens_superseded_by_idx;
DROP INDEX IF EXISTS refresh_tokens_expires_at_idx;
