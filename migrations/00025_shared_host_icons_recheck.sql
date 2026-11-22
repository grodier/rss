-- +goose Up
-- Sites on a shared host (a key with a path) now take their own picture
-- before the platform's icon (#744): check the ones stored now soon, rather
-- than up to a month from now. Only when to check changes.
UPDATE sites SET icon_next_check_at = now()
WHERE position('/' in host) > 0 AND icon_next_check_at > now();

-- +goose Down
SELECT 1;
