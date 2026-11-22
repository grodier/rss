-- +goose Up
-- Sites on a shared host (a key with a path) now take their own picture
-- before the platform's icon (#744): check the ones stored now within a few
-- minutes, rather than up to a month from now. Not at once: www migrate runs
-- while the old version still serves, and its icon workers would check them
-- by the old rules and set the next check a month away. Only when to check
-- changes.
UPDATE sites SET icon_next_check_at = now() + interval '10 minutes'
WHERE position('/' in host) > 0 AND icon_next_check_at > now() + interval '10 minutes';

-- +goose Down
SELECT 1;
