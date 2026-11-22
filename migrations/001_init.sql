-- +goose Up
CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE entries (
    id         SERIAL PRIMARY KEY,
    name       CITEXT NOT NULL UNIQUE,
    pick_count INTEGER NOT NULL DEFAULT 0,
    excluded   BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE picks (
    id          SERIAL PRIMARY KEY,
    entry_id    INTEGER NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
    picked_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    pick_number INTEGER NOT NULL
);

CREATE INDEX idx_entries_pick_count ON entries(pick_count) WHERE NOT excluded;
CREATE INDEX idx_picks_entry ON picks(entry_id);

CREATE TABLE tags (
    id   SERIAL PRIMARY KEY,
    name CITEXT NOT NULL UNIQUE
);

CREATE TABLE entry_tags (
    entry_id INTEGER NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
    tag_id   INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (entry_id, tag_id)
);

CREATE INDEX idx_entry_tags_tag ON entry_tags(tag_id);

CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

INSERT INTO entries (name, pick_count)
SELECT 'contestant ' || i, 0
FROM generate_series(1, 12) AS i
WHERE NOT EXISTS (SELECT 1 FROM entries LIMIT 1);

INSERT INTO settings (key, value) VALUES ('list_name', 'Entries');

-- +goose Down
DROP TABLE entry_tags;
DROP TABLE picks;
DROP TABLE tags;
DROP TABLE entries;
DROP TABLE settings;
