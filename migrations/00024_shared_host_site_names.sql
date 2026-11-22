-- +goose Up
-- A site on a shared host (a key with a path) is named as it names itself
-- (#728): its stored title loses the platform's suffix, a trailing handle,
-- Medium's "Stories by … on Medium" and GitHub's "GitHub - " and
-- description, as discovery.OwnName does for every lookup from now on. A
-- title that would end up empty is left as it is. Ordinary sites are
-- untouched.
WITH steps AS (
    SELECT id, host, split_part(host, '/', 1) = 'github.com' AS github,
        regexp_replace(btrim(title), '^Stories by (.+) on Medium$', '\1') AS t
    FROM sites
    WHERE position('/' in host) > 0 AND title <> ''
), github_affixes AS (
    SELECT id, github,
        CASE WHEN github THEN regexp_replace(regexp_replace(t, '^GitHub - ', ''), ' · GitHub$', '') ELSE t END AS t
    FROM steps
), suffixes AS (
    SELECT id, github,
        regexp_replace(regexp_replace(t, '\s+[-–—|]\s+[^-–—|]*$', ''), '\s*\(@[^()]*\)$', '') AS t
    FROM github_affixes
), names AS (
    SELECT id, btrim(CASE WHEN github THEN split_part(t, ': ', 1) ELSE t END) AS name
    FROM suffixes
)
UPDATE sites s SET title = n.name, updated_at = now()
FROM names n
WHERE s.id = n.id AND n.name <> '' AND n.name <> s.title;

-- +goose Down
-- The original titles aren't kept; a home-page lookup sets them again.
SELECT 1;
