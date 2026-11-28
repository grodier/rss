-- +goose Up
-- Feeds stored at a platform's alias host move to its main host (#851), as
-- discovery.CanonicalFeedURL now stores them, unless that URL is already
-- another feed's (merging two feeds' followers is its own change).
UPDATE feeds f SET url = regexp_replace(f.url, '^(https?://)m\.youtube\.com/', '\1www.youtube.com/')
WHERE f.url ~ '^https?://m\.youtube\.com/'
  AND NOT EXISTS (SELECT 1 FROM feeds g WHERE g.url = regexp_replace(f.url, '^(https?://)m\.youtube\.com/', '\1www.youtube.com/'));

UPDATE feeds f SET url = regexp_replace(f.url, '^(https?://)old\.reddit\.com/', '\1www.reddit.com/')
WHERE f.url ~ '^https?://old\.reddit\.com/'
  AND NOT EXISTS (SELECT 1 FROM feeds g WHERE g.url = regexp_replace(f.url, '^(https?://)old\.reddit\.com/', '\1www.reddit.com/'));

UPDATE feeds f SET url = regexp_replace(f.url, '^(https?://)new\.reddit\.com/', '\1www.reddit.com/')
WHERE f.url ~ '^https?://new\.reddit\.com/'
  AND NOT EXISTS (SELECT 1 FROM feeds g WHERE g.url = regexp_replace(f.url, '^(https?://)new\.reddit\.com/', '\1www.reddit.com/'));

UPDATE feeds f SET url = regexp_replace(f.url, '^(https?://)np\.reddit\.com/', '\1www.reddit.com/')
WHERE f.url ~ '^https?://np\.reddit\.com/'
  AND NOT EXISTS (SELECT 1 FROM feeds g WHERE g.url = regexp_replace(f.url, '^(https?://)np\.reddit\.com/', '\1www.reddit.com/'));

UPDATE feeds f SET url = regexp_replace(f.url, '^(https?://)m\.reddit\.com/', '\1www.reddit.com/')
WHERE f.url ~ '^https?://m\.reddit\.com/'
  AND NOT EXISTS (SELECT 1 FROM feeds g WHERE g.url = regexp_replace(f.url, '^(https?://)m\.reddit\.com/', '\1www.reddit.com/'));

UPDATE feeds f SET url = regexp_replace(f.url, '^(https?://)i\.reddit\.com/', '\1www.reddit.com/')
WHERE f.url ~ '^https?://i\.reddit\.com/'
  AND NOT EXISTS (SELECT 1 FROM feeds g WHERE g.url = regexp_replace(f.url, '^(https?://)i\.reddit\.com/', '\1www.reddit.com/'));

-- +goose Down
-- Nothing: the old URLs are aliases of the new ones.
SELECT 1;
