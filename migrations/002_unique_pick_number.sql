-- Pick numbers must be unique. Deployed databases created before this
-- migration may hold duplicates (concurrent Draws could assign the same
-- number), so renumber every Pick 1..n in the order it was picked first.
-- On a database without duplicates this leaves the numbers unchanged.

-- +goose Up
UPDATE picks p
SET pick_number = r.n
FROM (SELECT id, row_number() OVER (ORDER BY picked_at, id) AS n FROM picks) r
WHERE p.id = r.id AND p.pick_number <> r.n;

ALTER TABLE picks ADD CONSTRAINT picks_pick_number_key UNIQUE (pick_number);

-- +goose Down
ALTER TABLE picks DROP CONSTRAINT picks_pick_number_key;
