-- +goose Up
-- Fix audit_log payload TEXT -> JSONB for queryability (GIN-eligible, @> filters)
-- Existing payload values are JSON strings emitted by audit_logger.go; cast via ::jsonb
ALTER TABLE audit_log ALTER COLUMN payload TYPE JSONB USING payload::jsonb;
-- Time-series: keep existing audit_log_created_at_idx btree, add BRIN for range scans / TTL deletes
CREATE INDEX IF NOT EXISTS audit_log_created_at_brin ON audit_log USING BRIN (created_at);

-- +goose Down
DROP INDEX IF EXISTS audit_log_created_at_brin;
ALTER TABLE audit_log ALTER COLUMN payload TYPE TEXT USING payload::text;
