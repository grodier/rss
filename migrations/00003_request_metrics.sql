-- +goose Up
-- Request counts per route, written by each www instance once a minute
-- (internal/metrics) and rolled up hourly; read by the admin.
CREATE TABLE request_metrics (
    period_start TIMESTAMPTZ NOT NULL, -- the minute or hour the row counts
    resolution TEXT NOT NULL CHECK (resolution IN ('minute', 'hour')),
    instance TEXT NOT NULL, -- os.Hostname() of the process; '' in hourly rows
    route TEXT NOT NULL,    -- chi route pattern, e.g. /feeds/{id}; '(unmatched)' outside every route
    method TEXT NOT NULL,
    status_class SMALLINT NOT NULL CHECK (status_class BETWEEN 1 AND 5), -- 2 for 2xx, …
    requests INT NOT NULL,
    duration_ms_sum DOUBLE PRECISION NOT NULL,
    bytes_sum BIGINT NOT NULL,
    buckets INT[] NOT NULL, -- requests per metrics.DurationBounds bucket, plus one above the last
    PRIMARY KEY (resolution, period_start, instance, route, method, status_class)
);

-- Go runtime and connection pool samples, one row per www instance a minute
-- (internal/metrics); read by the admin.
CREATE TABLE runtime_metrics (
    period_start TIMESTAMPTZ NOT NULL,
    instance TEXT NOT NULL,
    heap_bytes BIGINT NOT NULL,
    total_bytes BIGINT NOT NULL, -- memory the Go runtime holds from the OS
    goroutines INT NOT NULL,
    gc_pause_p99_ms DOUBLE PRECISION NOT NULL,
    db_open INT NOT NULL,
    db_in_use INT NOT NULL,
    db_wait_count BIGINT NOT NULL, -- cumulative, as sql.DBStats reports it
    db_wait_ms BIGINT NOT NULL,    -- cumulative
    PRIMARY KEY (period_start, instance)
);

-- +goose Down
DROP TABLE runtime_metrics;
DROP TABLE request_metrics;
