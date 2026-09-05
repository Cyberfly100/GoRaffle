-- +goose Up
INSERT INTO contestants (name, pick_count)
SELECT 'contestant ' || i, 0
FROM generate_series(1, 12) AS i
WHERE NOT EXISTS (SELECT 1 FROM contestants LIMIT 1);

-- +goose Down
DELETE FROM contestants WHERE name LIKE 'contestant %';
