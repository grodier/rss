-- +goose Up
-- A reader's newsletter address: mail to local_part@<-newsletter-domain> becomes their newsletters.
CREATE TABLE newsletter_addresses (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    local_part CITEXT NOT NULL UNIQUE, -- before the @, e.g. george.k7x2m9
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    retired_at TIMESTAMPTZ -- when the reader got a new address; mail to it is accepted for 30 days after
);
CREATE UNIQUE INDEX newsletter_addresses_current ON newsletter_addresses (user_id) WHERE retired_at IS NULL;

-- +goose Down
DROP TABLE newsletter_addresses;
