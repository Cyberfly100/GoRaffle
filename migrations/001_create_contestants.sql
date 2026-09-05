-- +goose Up
CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE contestants (
    id         SERIAL PRIMARY KEY,
    name       CITEXT NOT NULL UNIQUE,
    pick_count INTEGER NOT NULL DEFAULT 0,
    excluded   BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down
DROP TABLE contestants;
