-- +goose Up
-- Background jobs per runner and outcome, written by each www instance once
-- a minute (internal/metrics) and rolled up hourly; read by the admin.
CREATE TABLE job_metrics (
    period_start TIMESTAMPTZ NOT NULL,
    resolution TEXT NOT NULL CHECK (resolution IN ('minute', 'hour')),
    instance TEXT NOT NULL, -- '' in hourly rows
    runner TEXT NOT NULL CHECK (runner IN ('lookup', 'import', 'refresh', 'icon', 'enrich', 'mail')),
    outcome TEXT NOT NULL, -- per runner, e.g. refresh: saved, not_modified, gone, failed (see "Background work" in docs/architecture.md)
    jobs INT NOT NULL,
    duration_ms_sum DOUBLE PRECISION NOT NULL,
    buckets INT[] NOT NULL, -- jobs per metrics.DurationBounds bucket, plus one above the last
    PRIMARY KEY (resolution, period_start, instance, runner, outcome)
);

-- +goose Down
DROP TABLE job_metrics;
