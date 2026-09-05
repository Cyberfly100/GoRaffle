-- +goose Up
CREATE TABLE tags (
    id   SERIAL PRIMARY KEY,
    name CITEXT NOT NULL UNIQUE
);

CREATE TABLE contestant_tags (
    contestant_id INTEGER NOT NULL REFERENCES contestants(id) ON DELETE CASCADE,
    tag_id        INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (contestant_id, tag_id)
);

CREATE INDEX idx_contestant_tags_tag ON contestant_tags(tag_id);

-- +goose Down
DROP TABLE contestant_tags;
DROP TABLE tags;
