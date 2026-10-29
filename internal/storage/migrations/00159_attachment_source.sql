-- +goose Up
-- Where on a vendor item an attachment was found (decision P122): 'issue' (Linear), 'event' (Sentry), 'property' (a Notion row's
-- files property), 'page body' (a file block inside a Notion page), 'issue body' (a file uploaded to GitHub and linked in the
-- issue), 'finding' (an Aikido finding's links). '' for rows listed before.
ALTER TABLE integration_attachments ADD COLUMN source TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE integration_attachments DROP COLUMN source;
