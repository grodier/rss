-- +goose Up
-- Enrichment jobs exist only for feeds that are followed or in Discover's
-- catalog (#708). Drop the jobs of every other feed, which
-- EnrichRepository.ClaimNext never claimed: a feed looked up but never
-- followed, or one whose last follower left. Then queue the backlog of the
-- catalog's feeds nobody follows, which were never enriched: their 20
-- latest articles that need it (queueEnrichBacklog's conditions), at the
-- backlog's priority.
DELETE FROM enrich_jobs j USING articles a
WHERE a.id = j.article_id
  AND NOT EXISTS (SELECT 1 FROM subscriptions s WHERE s.feed_id = a.feed_id)
  AND NOT EXISTS (SELECT 1 FROM discover_feeds d WHERE d.feed_id = a.feed_id);

INSERT INTO enrich_jobs (article_id, priority)
SELECT id, 1 FROM (
    SELECT a.id, row_number() OVER (PARTITION BY a.feed_id ORDER BY a.timeline_at DESC, a.id DESC) AS n
    FROM articles a
    JOIN discover_feeds d ON d.feed_id = a.feed_id
    WHERE NOT EXISTS (SELECT 1 FROM subscriptions s WHERE s.feed_id = a.feed_id)
      AND a.url <> '' AND (a.image_url = '' OR a.title = '' OR a.excerpt = '')
      AND a.page_image_url = '' AND a.page_title = '' AND a.page_description = ''
) latest
WHERE n <= 20
ON CONFLICT (article_id) DO NOTHING;

-- +goose Down
-- Nothing to restore: following a feed or putting it in Discover's catalog
-- queues its jobs.
SELECT 1;
