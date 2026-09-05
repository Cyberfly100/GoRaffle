-- +goose Up
CREATE TABLE picks (
    id            SERIAL PRIMARY KEY,
    contestant_id INTEGER NOT NULL REFERENCES contestants(id) ON DELETE CASCADE,
    picked_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    pick_number   INTEGER NOT NULL
);

CREATE INDEX idx_contestants_pick_count ON contestants(pick_count) WHERE NOT excluded;
CREATE INDEX idx_picks_contestant ON picks(contestant_id);

-- +goose Down
DROP TABLE picks;
