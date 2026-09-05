package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/lib/pq"

	"github.com/goraffle/raffle/internal/model"
)

var ErrNotFound = errors.New("not found")

// Ping verifies the database connection is healthy.
func (s *Store) Ping(ctx context.Context) error {
	return s.DB.PingContext(ctx)
}

// querier spans *sql.DB and *sql.Tx for the operations we need.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type Store struct {
	DB *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{DB: db}
}

// ListEntries returns all entries with their tags, ordered by name.
func (s *Store) ListEntries(ctx context.Context) ([]model.Entry, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT c.id, c.name, c.pick_count, c.excluded,
		       COALESCE(array_agg(t.name ORDER BY t.name) FILTER (WHERE t.name IS NOT NULL), '{}'::text[]),
		       COALESCE(array_agg(t.id ORDER BY t.name) FILTER (WHERE t.id IS NOT NULL), '{}'::int[])
		FROM entries c
		LEFT JOIN entry_tags ct ON ct.entry_id = c.id
		LEFT JOIN tags t ON t.id = ct.tag_id
		GROUP BY c.id, c.name, c.pick_count, c.excluded
		ORDER BY c.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.Entry
	for rows.Next() {
		var c model.Entry
		var tags []string
		var tagIDs64 []int64
		if err := rows.Scan(&c.ID, &c.Name, &c.PickCount, &c.Excluded, pq.Array(&tags), pq.Array(&tagIDs64)); err != nil {
			return nil, err
		}
		c.Tags = tags
		c.TagIDs = toInts(tagIDs64)
		if c.Tags == nil {
			c.Tags = []string{}
		}
		if c.TagIDs == nil {
			c.TagIDs = []int{}
		}
		out = append(out, c)
	}
	if out == nil {
		out = []model.Entry{}
	}
	return out, rows.Err()
}

func toInts(v []int64) []int {
	out := make([]int, len(v))
	for i, x := range v {
		out[i] = int(x)
	}
	return out
}

// AddEntry inserts a entry. If one with the same name exists it is
// returned unchanged (case-insensitive via citext). Returns the created/existing entry.
func (s *Store) AddEntry(ctx context.Context, tx *sql.Tx, name string) (model.Entry, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return model.Entry{}, errors.New("name must not be empty")
	}

	q := querier(s.DB)
	if tx != nil {
		q = tx
	}

	var c model.Entry
	err := q.QueryRowContext(ctx, `
		INSERT INTO entries (name) VALUES ($1)
		ON CONFLICT (name) DO NOTHING
		RETURNING id, name, pick_count, excluded`, name).
		Scan(&c.ID, &c.Name, &c.PickCount, &c.Excluded)
	if errors.Is(err, sql.ErrNoRows) {
		// Already exists; fetch it.
		err = q.QueryRowContext(ctx,
			`SELECT id, name, pick_count, excluded FROM entries WHERE name = $1`, name).
			Scan(&c.ID, &c.Name, &c.PickCount, &c.Excluded)
	}
	if err != nil {
		return model.Entry{}, err
	}
	c.Tags = []string{}
	return c, nil
}

// GetEntry returns a single entry by id with tags.
func (s *Store) GetEntry(ctx context.Context, id int) (model.Entry, error) {
	var c model.Entry
	var tags []string
	var tagIDs64 []int64
	err := s.DB.QueryRowContext(ctx, `
		SELECT c.id, c.name, c.pick_count, c.excluded,
		       COALESCE(array_agg(t.name ORDER BY t.name) FILTER (WHERE t.name IS NOT NULL), '{}'::text[]),
		       COALESCE(array_agg(t.id ORDER BY t.name) FILTER (WHERE t.id IS NOT NULL), '{}'::int[])
		FROM entries c
		LEFT JOIN entry_tags ct ON ct.entry_id = c.id
		LEFT JOIN tags t ON t.id = ct.tag_id
		WHERE c.id = $1
		GROUP BY c.id, c.name, c.pick_count, c.excluded`, id).
		Scan(&c.ID, &c.Name, &c.PickCount, &c.Excluded, pq.Array(&tags), pq.Array(&tagIDs64))
	if errors.Is(err, sql.ErrNoRows) {
		return model.Entry{}, ErrNotFound
	}
	if err != nil {
		return model.Entry{}, err
	}
	if tags == nil {
		tags = []string{}
	}
	c.Tags = tags
	c.TagIDs = toInts(tagIDs64)
	if c.TagIDs == nil {
		c.TagIDs = []int{}
	}
	return c, nil
}

// UpdateEntry updates name/count/excluded for a entry.
func (s *Store) UpdateEntry(ctx context.Context, c model.Entry) error {
	res, err := s.DB.ExecContext(ctx, `
		UPDATE entries
		SET name = $2,
		    pick_count = $3,
		    excluded = $4,
		    updated_at = NOW()
		WHERE id = $1`, c.ID, c.Name, c.PickCount, c.Excluded)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteEntry removes a entry and their tag links / picks (cascades).
func (s *Store) DeleteEntry(ctx context.Context, id int) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM entries WHERE id = $1`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ClearEntries deletes every entry and, transitively, all picks and tag links.
func (s *Store) ClearEntries(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM entries`)
	return err
}

// eligibleWhere is the tag-filter predicate shared by both eligible-pool
// queries. $1 = mustHave (ALL required), $2 = anyOf (at least one).
const eligibleWhere = `
  AND (
    array_length($1::text[], 1) IS NULL
    OR NOT EXISTS (
      SELECT 1 FROM unnest($1::text[]) AS req
      WHERE NOT EXISTS (
        SELECT 1 FROM entry_tags ct
        JOIN tags t ON ct.tag_id = t.id
        WHERE ct.entry_id = c.id AND t.name = req
      )
    )
  )
  AND (
    array_length($2::text[], 1) IS NULL
    OR EXISTS (
      SELECT 1 FROM entry_tags ct
      JOIN tags t ON ct.tag_id = t.id
      WHERE ct.entry_id = c.id AND t.name = ANY($2::text[])
    )
  )`

// EligiblePool returns the ids of non-excluded entries that pass the tag filter
// (mustHave = ALL required, anyOf = at least one; empty both = no filter).
func (s *Store) EligiblePool(ctx context.Context, mustHave, anyOf []string) ([]int, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT c.id
		FROM entries c
		WHERE c.excluded = FALSE`+eligibleWhere, toNullArray(mustHave), toNullArray(anyOf))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// EligibleEntries returns the full entries that pass the tag filter and
// are not excluded (used for the suspense animation pool).
func (s *Store) EligibleEntries(ctx context.Context, mustHave, anyOf []string) ([]model.Entry, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT c.id, c.name, c.pick_count, c.excluded
		FROM entries c
		WHERE c.excluded = FALSE`+eligibleWhere+`
		ORDER BY c.name`, toNullArray(mustHave), toNullArray(anyOf))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.Entry
	for rows.Next() {
		var c model.Entry
		if err := rows.Scan(&c.ID, &c.Name, &c.PickCount, &c.Excluded); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if out == nil {
		out = []model.Entry{}
	}
	return out, rows.Err()
}

// DrawLeaderboard returns ids + pick_count for a set of ids ordered by count ascending,
// so the engine can pick among the minimum-count ties.
func (s *Store) DrawLeaderboard(ctx context.Context, ids []int) (map[int]int, error) {
	if len(ids) == 0 {
		return map[int]int{}, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}
	query := fmt.Sprintf(`SELECT id, pick_count FROM entries WHERE id IN (%s)`,
		strings.Join(placeholders, ","))
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[int]int, len(ids))
	for rows.Next() {
		var id, count int
		if err := rows.Scan(&id, &count); err != nil {
			return nil, err
		}
		out[id] = count
	}
	return out, rows.Err()
}

// RecordPick inserts a pick and increments the winner's count atomically.
// The pick number is derived inside the transaction as MAX(pick_number)+1.
// Returns the new count, the assigned pick number, and the pick id.
func (s *Store) RecordPick(ctx context.Context, tx *sql.Tx, entryID int) (int, int, int, error) {
	q := querier(s.DB)
	if tx != nil {
		q = tx
	}

	if _, err := q.ExecContext(ctx,
		`SELECT 1 FROM entries WHERE id = $1 FOR UPDATE`, entryID); err != nil {
		return 0, 0, 0, err
	}

	var next int
	if err := q.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(pick_number), 0) + 1 FROM picks`).Scan(&next); err != nil {
		return 0, 0, 0, err
	}

	var pickID int
	if err := q.QueryRowContext(ctx,
		`INSERT INTO picks (entry_id, pick_number) VALUES ($1, $2)
		 RETURNING id`, entryID, next).Scan(&pickID); err != nil {
		return 0, 0, 0, err
	}

	var count int
	err := q.QueryRowContext(ctx,
		`UPDATE entries SET pick_count = pick_count + 1, updated_at = NOW()
		 WHERE id = $1 RETURNING pick_count`, entryID).Scan(&count)
	return count, next, pickID, err
}

// LastPick returns the most recent pick with the winner name, or ErrNotFound.
func (s *Store) LastPick(ctx context.Context) (model.Pick, error) {
	var p model.Pick
	err := s.DB.QueryRowContext(ctx, `
		SELECT p.id, p.entry_id, c.name, p.pick_number, p.picked_at
		FROM picks p JOIN entries c ON c.id = p.entry_id
		ORDER BY p.id DESC LIMIT 1`).
		Scan(&p.ID, &p.EntryID, &p.EntryName, &p.PickNumber, &p.PickedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Pick{}, ErrNotFound
	}
	return p, err
}

// UndoLastPick atomically removes the last pick and decrements the winner's count.
func (s *Store) UndoLastPick(ctx context.Context) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var entryID, pickNumber int
	err = tx.QueryRowContext(ctx,
		`SELECT entry_id, pick_number FROM picks ORDER BY id DESC LIMIT 1 FOR UPDATE`).
		Scan(&entryID, &pickNumber)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM picks WHERE id = (
		   SELECT id FROM picks ORDER BY id DESC LIMIT 1)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE entries SET pick_count = pick_count - 1, updated_at = NOW() WHERE id = $1`,
		entryID); err != nil {
		return err
	}
	return tx.Commit()
}

// ListHistory returns all picks (newest first) with names.
func (s *Store) ListHistory(ctx context.Context) ([]model.Pick, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT p.id, p.entry_id, c.name, p.pick_number, p.picked_at
		FROM picks p JOIN entries c ON c.id = p.entry_id
		ORDER BY p.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.Pick
	for rows.Next() {
		var p model.Pick
		if err := rows.Scan(&p.ID, &p.EntryID, &p.EntryName, &p.PickNumber, &p.PickedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if out == nil {
		out = []model.Pick{}
	}
	return out, rows.Err()
}

// ImportEntries upserts entries by name inside one transaction.
// Returns counts of added and updated rows. Existing names' count/excluded are
// updated only when the import supplies them; tags are replaced when supplied.
func (s *Store) ImportEntries(ctx context.Context, in []model.EntryImport) (added, updated int, err error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	for _, ci := range in {
		name := strings.TrimSpace(ci.Name)
		if name == "" {
			continue
		}

		var id int
		exists := true
		err := tx.QueryRowContext(ctx,
			`SELECT id FROM entries WHERE name = $1`, name).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			exists = false
		} else if err != nil {
			return added, updated, err
		}

		count := 0
		if ci.PickCount != nil {
			count = *ci.PickCount
		}
		if !exists {
			err = tx.QueryRowContext(ctx,
				`INSERT INTO entries (name, pick_count, excluded)
				 VALUES ($1, $2, $3) RETURNING id`,
				name, count, boolOr(ci.Excluded, false)).Scan(&id)
			if err != nil {
				return added, updated, err
			}
			added++
		} else {
			if ci.PickCount != nil || ci.Excluded != nil {
				_, err = tx.ExecContext(ctx,
					`UPDATE entries
					 SET pick_count = COALESCE($2, pick_count),
					     excluded = COALESCE($3, excluded),
					     updated_at = NOW()
					 WHERE id = $1`,
					id, ci.PickCount, ci.Excluded)
				if err != nil {
					return added, updated, err
				}
			}
			updated++
		}

		if len(ci.Tags) > 0 {
			if err := s.SetEntryTags(ctx, tx, id, ci.Tags); err != nil {
				return added, updated, err
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return added, updated, nil
}

func boolOr(b *bool, def bool) bool {
	if b != nil {
		return *b
	}
	return def
}

// ResetScores zeroes all counts and clears pick history in a transaction.
func (s *Store) ResetScores(ctx context.Context) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE entries SET pick_count = 0, updated_at = NOW()`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM picks`); err != nil {
		return err
	}
	return tx.Commit()
}

// ---- Tags ----

// ListTags returns all tags ever used, ordered by name.
func (s *Store) ListTags(ctx context.Context) ([]model.Tag, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, name FROM tags ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.Tag
	for rows.Next() {
		var t model.Tag
		if err := rows.Scan(&t.ID, &t.Name); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if out == nil {
		out = []model.Tag{}
	}
	return out, rows.Err()
}

// GetEntryTags returns the tags currently assigned to a entry.
func (s *Store) GetEntryTags(ctx context.Context, entryID int) ([]model.Tag, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT t.id, t.name FROM tags t
		JOIN entry_tags ct ON ct.tag_id = t.id
		WHERE ct.entry_id = $1 ORDER BY t.name`, entryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.Tag
	for rows.Next() {
		var t model.Tag
		if err := rows.Scan(&t.ID, &t.Name); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if out == nil {
		out = []model.Tag{}
	}
	return out, rows.Err()
}

// TagByName returns the id of a tag, creating it if needed. Operates on the
// supplied querier so callers can run it on a transaction when one is open.
func (s *Store) TagByName(ctx context.Context, q querier, name string) (int, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, errors.New("tag name must not be empty")
	}
	var id int
	err := q.QueryRowContext(ctx, `
		INSERT INTO tags (name) VALUES ($1)
		ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name
		RETURNING id`, name).Scan(&id)
	return id, err
}

// AddTagToEntry links a tag (creating it if necessary) to a entry.
func (s *Store) AddTagToEntry(ctx context.Context, entryID int, tagName string) error {
	tagID, err := s.TagByName(ctx, s.DB, tagName)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `
		INSERT INTO entry_tags (entry_id, tag_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, entryID, tagID)
	return err
}

// RemoveTagFromEntry removes a tag link by tag id.
func (s *Store) RemoveTagFromEntry(ctx context.Context, entryID, tagID int) error {
	res, err := s.DB.ExecContext(ctx,
		`DELETE FROM entry_tags WHERE entry_id = $1 AND tag_id = $2`,
		entryID, tagID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetEntryTags replaces the full tag set for a entry (used by import).
func (s *Store) SetEntryTags(ctx context.Context, tx *sql.Tx, entryID int, tags []string) error {
	q := querier(s.DB)
	if tx != nil {
		q = tx
	}
	if _, err := q.ExecContext(ctx,
		`DELETE FROM entry_tags WHERE entry_id = $1`, entryID); err != nil {
		return err
	}
	for _, t := range tags {
		tagID, err := s.TagByName(ctx, q, t)
		if err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx,
			`INSERT INTO entry_tags (entry_id, tag_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			entryID, tagID); err != nil {
			return err
		}
	}
	return nil
}

func toNullArray(v []string) any {
	if v == nil {
		return nil
	}
	return v
}
