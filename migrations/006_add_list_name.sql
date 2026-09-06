-- +goose Up
CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

INSERT INTO settings (key, value) VALUES ('list_name', 'Entries');

-- +goose Down
DROP TABLE settings;