-- +goose Up
-- One row per page view from the real-user beacon (POST /rum), kept 30 days,
-- then only in rum_daily. Carries no identifiers: no user, IP or URL.
CREATE TABLE rum_views (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    route TEXT NOT NULL, -- chi route pattern of the page's path; never the path
    nav_type TEXT NOT NULL CHECK (nav_type IN ('navigate', 'reload', 'back_forward', 'prerender', 'restore')),
    device TEXT NOT NULL CHECK (device IN ('phone', 'tablet', 'desktop')),
    browser TEXT NOT NULL,         -- family; 'other' when unknown
    browser_major INT NOT NULL DEFAULT 0,
    installed BOOLEAN NOT NULL,    -- display-mode: standalone
    connection TEXT NOT NULL DEFAULT '', -- effectiveType, where the browser reports it
    protocol TEXT NOT NULL DEFAULT '',   -- nextHopProtocol: h2, h3, http/1.1
    referrer_host TEXT NOT NULL DEFAULT '', -- another site's host; '' for none or our own
    ttfb_ms REAL, fcp_ms REAL, lcp_ms REAL, inp_ms REAL, cls REAL, -- NULL when not reported
    dcl_ms REAL, load_ms REAL,
    server_ms REAL,      -- Server-Timing app;dur, when present
    transfer_bytes INT,
    lcp_element TEXT NOT NULL DEFAULT '', -- tag name of the LCP element
    inp_target TEXT NOT NULL DEFAULT ''   -- data-* hook or tag name of the slowest interaction's target
);

CREATE INDEX rum_views_received_at_idx ON rum_views (received_at);

-- Daily rollups of rum_views, kept 13 months.
CREATE TABLE rum_daily (
    day DATE NOT NULL, -- the UTC day the views were received
    route TEXT NOT NULL,
    device TEXT NOT NULL,
    browser TEXT NOT NULL,
    metric TEXT NOT NULL CHECK (metric IN ('ttfb', 'fcp', 'lcp', 'inp', 'cls', 'server')),
    views INT NOT NULL,
    buckets INT[] NOT NULL, -- views per metrics.RUMBounds[metric] bucket, plus one above the last
    PRIMARY KEY (day, route, device, browser, metric)
);

-- +goose Down
DROP TABLE rum_daily;
DROP TABLE rum_views;
