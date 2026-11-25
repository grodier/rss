-- +goose Up
-- When a reader's browser first reported that the article's feed image
-- (image_url) failed to load; NULL if none has. The first report queues the
-- article's web page for enrichment, so the page's image can stand in; a
-- changed image_url clears it. See ArticleRepository.ReportImageFailed.
ALTER TABLE articles ADD COLUMN image_failed_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE articles DROP COLUMN image_failed_at;
