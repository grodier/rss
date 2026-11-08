-- +goose Up
-- The pre-alpha reset check's table (#217), which psql.Migrate created
-- outside the migrations; resets ended with this migration.
DROP TABLE IF EXISTS schema_fingerprint;

-- +goose Down
