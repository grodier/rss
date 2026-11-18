-- +goose Up
-- Light is now Paper's palette, so Paper is no longer a choice: accounts
-- that chose it get Light, which looks the same. The check still allows
-- 'paper' while the previous version, which can save it, runs beside this
-- one during the deploy; a later migration converts any it saved and drops
-- it from the check (backlog.md).
UPDATE users SET theme = 'light' WHERE theme = 'paper';

-- +goose Down
-- Nothing to undo: which accounts chose Paper rather than Light is gone.
SELECT 1;
