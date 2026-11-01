-- +goose Up
-- The page of a PDF a pack passage came from (decision P110): 0 for every other source, which has no pages. A citation names
-- the page, so an answer drawn from a 300-page manual says where to look. The heading path of such a passage also ends in
-- "Page N", so search weights it and older rows need nothing filled in.
ALTER TABLE pack_chunks ADD COLUMN page INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE pack_chunks DROP COLUMN page;
