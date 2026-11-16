-- +goose Up
-- Reading settings: a third light-or-dark choice, Paper (a warm light
-- theme), and how article rows are laid out. Existing accounts get
-- Headlines, the new default; Cards is the layout they had.
ALTER TABLE users DROP CONSTRAINT users_theme_check;
ALTER TABLE users ADD CONSTRAINT users_theme_check CHECK (theme IN ('system', 'light', 'paper', 'dark'));
ALTER TABLE users ADD COLUMN layout TEXT NOT NULL DEFAULT 'headlines' CHECK (layout IN ('headlines', 'cards')); -- how article rows are drawn

-- +goose Down
ALTER TABLE users DROP COLUMN layout;
UPDATE users SET theme = 'light' WHERE theme = 'paper';
ALTER TABLE users DROP CONSTRAINT users_theme_check;
ALTER TABLE users ADD CONSTRAINT users_theme_check CHECK (theme IN ('system', 'light', 'dark'));
