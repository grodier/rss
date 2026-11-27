-- +goose Up
-- Raw emails received for readers' addresses, waiting for the newsletter runner.
CREATE TABLE inbound_emails (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    address_id UUID NOT NULL REFERENCES newsletter_addresses(id) ON DELETE CASCADE,
    raw BYTEA NOT NULL,                                  -- the message as received, at most 5 MiB
    digest BYTEA NOT NULL,                               -- sha256 of raw: a replay of a pending email is refused
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    attempts INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(), -- when the runner may claim it; doubles as the claim's lease
    last_error TEXT NOT NULL DEFAULT ''                  -- why processing last failed, never with header or body text. Internal detail, never shown to users
);
CREATE INDEX inbound_emails_due ON inbound_emails (next_attempt_at);
CREATE UNIQUE INDEX inbound_emails_digest ON inbound_emails (address_id, digest);

-- inbound: POST /inbound/email's requests; newsletter: the runner that processes inbound_emails.
ALTER TABLE job_metrics DROP CONSTRAINT job_metrics_runner_check;
ALTER TABLE job_metrics ADD CONSTRAINT job_metrics_runner_check
    CHECK (runner IN ('lookup', 'import', 'refresh', 'icon', 'enrich', 'mail', 'inbound', 'newsletter'));

-- +goose Down
DELETE FROM job_metrics WHERE runner IN ('inbound', 'newsletter');
ALTER TABLE job_metrics DROP CONSTRAINT job_metrics_runner_check;
ALTER TABLE job_metrics ADD CONSTRAINT job_metrics_runner_check
    CHECK (runner IN ('lookup', 'import', 'refresh', 'icon', 'enrich', 'mail'));
DROP TABLE inbound_emails;
