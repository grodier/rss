-- +goose Up
-- Paper is no longer a theme (00016): convert any account the previous
-- version saved with it during that deploy, then stop allowing it. The
-- version running beside this one never saves 'paper'.
UPDATE users SET theme = 'light' WHERE theme = 'paper';
ALTER TABLE users DROP CONSTRAINT users_theme_check;
ALTER TABLE users ADD CONSTRAINT users_theme_check CHECK (theme IN ('system', 'light', 'dark'));

-- +goose Down
ALTER TABLE users DROP CONSTRAINT users_theme_check;
ALTER TABLE users ADD CONSTRAINT users_theme_check CHECK (theme IN ('system', 'light', 'paper', 'dark'));
