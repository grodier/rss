-- +goose Up
-- The admin's checks of www's /healthcheck over the private network, once a
-- minute; kept 90 days.
CREATE TABLE availability_checks (
    checked_at TIMESTAMPTZ NOT NULL PRIMARY KEY,
    ok BOOLEAN NOT NULL,        -- answered "available" in time
    status TEXT NOT NULL,       -- the body's status, or 'timeout', 'error'
    latency_ms REAL,            -- NULL when it didn't answer
    version TEXT NOT NULL DEFAULT '' -- the body's system_info.version
);

-- +goose Down
DROP TABLE availability_checks;
