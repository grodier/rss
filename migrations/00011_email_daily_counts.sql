-- +goose Up
-- Emails queued per UTC day, for the admin's sending limits (the outbox
-- deletes finished emails after 7 days, so a month can't be counted from
-- emails). Incremented by EmailRepository.Enqueue in the same statement as
-- the insert.
CREATE TABLE email_daily_counts (
    day DATE PRIMARY KEY,
    queued INT NOT NULL
);

-- The emails still in the outbox: the days before them are past counting.
INSERT INTO email_daily_counts (day, queued)
SELECT (created_at AT TIME ZONE 'UTC')::date, count(*)
FROM emails
GROUP BY 1;

-- +goose Down
DROP TABLE email_daily_counts;
