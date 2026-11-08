-- +goose Up
-- The most recent 500s and panics in www, newest kept (internal/server
-- serverErrorHTML and serverErrorJSON); read by the admin.
CREATE TABLE server_errors (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    instance TEXT NOT NULL,  -- os.Hostname() of the process
    route TEXT NOT NULL,     -- chi route pattern; never the URL
    method TEXT NOT NULL,
    message TEXT NOT NULL,   -- err.Error(), cut to 2,000 bytes; internal detail, shown only in the admin
    panic BOOLEAN NOT NULL DEFAULT false
);

CREATE INDEX server_errors_route_idx ON server_errors (route, occurred_at DESC);

-- +goose Down
DROP TABLE server_errors;
