-- +goose Up
-- A site on a shared host (a key with a path) is named as it names itself
-- (#728), as discovery.OwnName names it in every lookup from now on. A
-- title taken from one of the site's feeds is the feed's own name, so it
-- loses only Medium's "Stories by … on Medium"; any other title came from
-- the platform's page and also loses the platform's suffix, a trailing
-- handle and, on GitHub, "GitHub - ", " · GitHub" and a repository's
-- description. A title that would end up empty is left as it is.
-- Ordinary sites are untouched.
WITH steps AS (
    SELECT s.id,
        split_part(s.host, '/', 1) = 'github.com' AS github,
        s.host ~ '^[^/]+/[^/]+/[^/]+$' AS repo,
        EXISTS (SELECT 1 FROM feeds f WHERE f.site_id = s.id AND f.title = s.title) AS from_feed,
        regexp_replace(regexp_replace(s.title, '^\s+|\s+$', '', 'g'), '^Stories by (.+) on Medium$', '\1') AS t
    FROM sites s
    WHERE position('/' in s.host) > 0 AND s.title <> ''
), github_affixes AS (
    SELECT id, github, repo, from_feed,
        CASE WHEN github AND NOT from_feed THEN regexp_replace(regexp_replace(t, '^GitHub - ', ''), ' · GitHub$', '') ELSE t END AS t
    FROM steps
), suffixes AS (
    SELECT id, github, repo, from_feed,
        CASE WHEN from_feed THEN t
            ELSE regexp_replace(regexp_replace(t, '\s+[-–—|]\s+[^-–—|]*$', ''), '\s*\(@[^()]*\)$', '') END AS t
    FROM github_affixes
), names AS (
    SELECT id,
        regexp_replace(CASE WHEN github AND repo AND NOT from_feed THEN split_part(t, ': ', 1) ELSE t END,
            '^\s+|\s+$', '', 'g') AS name
    FROM suffixes
)
UPDATE sites s SET title = n.name, updated_at = now()
FROM names n
WHERE s.id = n.id AND n.name <> '' AND n.name <> s.title;

-- +goose Down
-- The original titles aren't kept; a home-page lookup sets them again.
SELECT 1;
