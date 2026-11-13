-- Three days of the pinger's checks and www's runtime samples, for the
-- service page's screenshots: an outage 2 days ago (timeouts, then errors),
-- a degraded minute 30 hours ago, and www down for the last 3 minutes.
DELETE FROM availability_checks;
DELETE FROM runtime_metrics;

INSERT INTO availability_checks (checked_at, ok, status, latency_ms, version)
SELECT t,
       NOT failed,
       CASE WHEN NOT failed THEN 'available'
            WHEN t > now() - interval '30 hours 1 minute' AND t <= now() - interval '30 hours' THEN 'degraded'
            WHEN t < now() - interval '2 days' + interval '3 minutes' THEN 'timeout'
            ELSE 'error' END,
       CASE WHEN NOT failed THEN 2 + random() * 6
            WHEN t > now() - interval '30 hours 1 minute' AND t <= now() - interval '30 hours' THEN 40 END,
       CASE WHEN failed AND NOT (t > now() - interval '30 hours 1 minute' AND t <= now() - interval '30 hours') THEN ''
            WHEN t < now() - interval '1 day' THEN '3f9c2a1b7d4e5f60718293a4b5c6d7e8f9012345'
            ELSE '8a7b6c5d4e3f2a1b0c9d8e7f6a5b4c3d2e1f0a9b' END
FROM (
    SELECT t,
           (t >= now() - interval '2 days' AND t < now() - interval '2 days' + interval '9 minutes')
           OR (t > now() - interval '30 hours 1 minute' AND t <= now() - interval '30 hours')
           OR t > now() - interval '3 minutes' AS failed
    FROM generate_series(date_trunc('minute', now()) - interval '3 days', date_trunc('minute', now()), interval '1 minute') AS t
) s;

-- One instance, replaced a day ago by a deploy (both run for 2 minutes).
INSERT INTO runtime_metrics (period_start, instance, heap_bytes, total_bytes, goroutines, gc_pause_p99_ms,
                             db_open, db_in_use, db_wait_count, db_wait_ms)
SELECT t, inst,
       ((38 + 14 * sin(extract(epoch FROM t) / 5400) + random() * 6) * 1048576)::bigint,
       ((96 + 8 * sin(extract(epoch FROM t) / 5400) + random() * 3) * 1048576)::bigint,
       (28 + 6 * sin(extract(epoch FROM t) / 3600) + random() * 5)::int,
       0.2 + random() * 0.6 + CASE WHEN random() < 0.01 THEN 2 ELSE 0 END,
       (3 + random() * 3)::int,
       (random() * 3)::int,
       (row_number() OVER (PARTITION BY inst ORDER BY t) / 90)::bigint,
       0
FROM generate_series(date_trunc('minute', now()) - interval '3 days', date_trunc('minute', now()) - interval '1 minute', interval '1 minute') AS t
CROSS JOIN (VALUES ('web-old'), ('web-new')) AS i(inst)
WHERE (inst = 'web-old' AND t < now() - interval '1 day' + interval '2 minutes')
   OR (inst = 'web-new' AND t >= now() - interval '1 day');
