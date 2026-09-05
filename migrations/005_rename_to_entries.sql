-- +goose Up
-- Rename the roster vocabulary from "contestant" to "entry" across the schema.
-- Postgres rewrites FK references automatically when a referenced table is
-- renamed, and renaming a column updates any FK that uses it. Index names are
-- left as-is (they do not affect behaviour).
ALTER TABLE contestants RENAME TO entries;
ALTER TABLE contestant_tags RENAME TO entry_tags;
ALTER TABLE picks RENAME COLUMN contestant_id TO entry_id;
ALTER TABLE entry_tags RENAME COLUMN contestant_id TO entry_id;

-- +goose Down
ALTER TABLE entry_tags RENAME COLUMN entry_id TO contestant_id;
ALTER TABLE picks RENAME COLUMN entry_id TO contestant_id;
ALTER TABLE entry_tags RENAME TO contestant_tags;
ALTER TABLE entries RENAME TO contestants;