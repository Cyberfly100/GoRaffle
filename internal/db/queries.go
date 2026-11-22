package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/lib/pq"

	"github.com/goraffle/raffle/internal/model"
)

var ErrNotFound = errors.New("not found")

// Ping verifies the database connection is healthy.
func (s *Store) Ping(ctx context.Context) error {
	return s.DB.PingContext(ctx)
}

// GetListName returns the current list name from settings.
func (s *Store) GetListName(ctx context.Context) (string, error) {
	var name string
	err := s.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'list_name'`).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "Entries", nil
	}
	return name, err
}

// SetListName updates the list name in settings.
func (s *Store) SetListName(ctx context.Context, name string) error {
	return setListName(ctx, s.DB, name)
}

func setListName(ctx context.Context, q querier, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Entries"
	}
	_, err := q.ExecContext(ctx, `
		INSERT INTO settings (key, value) VALUES ('list_name', $1)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, name)
	return err
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
	return listEntries(ctx, s.DB)
}

// EntriesAndPool returns every Entry and the Eligible pool under the Tag
// filter, both read from one snapshot so they always agree.
func (s *Store) EntriesAndPool(ctx context.Context, filter model.TagFilter) ([]model.Entry, []PoolEntry, error) {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	entries, err := listEntries(ctx, tx)
	if err != nil {
		return nil, nil, err
	}
	pool, err := eligiblePool(ctx, tx, filter)
	if err != nil {
		return nil, nil, err
	}
	return entries, pool, tx.Commit()
}

func listEntries(ctx context.Context, q querier) ([]model.Entry, error) {
	rows, err := q.QueryContext(ctx, `
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
	return getEntry(ctx, s.DB, id)
}

func getEntry(ctx context.Context, q querier, id int) (model.Entry, error) {
	var c model.Entry
	var tags []string
	var tagIDs64 []int64
	err := q.QueryRowContext(ctx, `
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

// eligibleWhere is the Tag filter predicate of the Eligible pool query.
// $1 = Must-have Tags (ALL required), $2 = any-of Tags (at least one).
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

// UndoLastPick atomically removes the last Pick and decrements the winner's
// Pick count, floored at zero so a hand-edited or imported count can never go
// negative. It is serialized against Draws and Reset. It returns the Pick it
// removed, read inside the same transaction, or ErrNotFound.
func (s *Store) UndoLastPick(ctx context.Context) (model.Pick, error) {
	tx, err := s.beginPicksTx(ctx)
	if err != nil {
		return model.Pick{}, err
	}
	defer tx.Rollback()

	var p model.Pick
	err = tx.QueryRowContext(ctx, `
		SELECT p.id, p.entry_id, c.name, p.pick_number, p.picked_at
		FROM picks p JOIN entries c ON c.id = p.entry_id
		ORDER BY p.id DESC LIMIT 1
		FOR UPDATE OF p`).
		Scan(&p.ID, &p.EntryID, &p.EntryName, &p.PickNumber, &p.PickedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Pick{}, ErrNotFound
	}
	if err != nil {
		return model.Pick{}, err
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM picks WHERE id = $1`, p.ID); err != nil {
		return model.Pick{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE entries SET pick_count = GREATEST(pick_count - 1, 0), updated_at = NOW() WHERE id = $1`,
		p.EntryID); err != nil {
		return model.Pick{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.Pick{}, err
	}
	return p, nil
}

// PickStands reports whether the Pick still exists (it has not been undone,
// reset, or removed with its Entry).
func (s *Store) PickStands(ctx context.Context, pickID int) (bool, error) {
	var ok bool
	err := s.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM picks WHERE id = $1)`, pickID).Scan(&ok)
	return ok, err
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

// ReadList returns the List: its name, every Entry with its Pick count,
// Excluded flag and Tags ordered by name, and the History as its Picks
// ordered by Pick number.
func (s *Store) ReadList(ctx context.Context) (model.List, error) {
	name, err := s.GetListName(ctx)
	if err != nil {
		return model.List{}, err
	}
	entries, err := s.ListEntries(ctx)
	if err != nil {
		return model.List{}, err
	}
	picks, err := s.listPicks(ctx)
	if err != nil {
		return model.List{}, err
	}
	l := model.List{
		Name:    name,
		Entries: make([]model.ListEntry, 0, len(entries)),
		Picks:   picks,
	}
	for _, e := range entries {
		l.Entries = append(l.Entries, model.ListEntry{
			Name:      e.Name,
			PickCount: &e.PickCount,
			Excluded:  &e.Excluded,
			Tags:      e.Tags,
		})
	}
	return l, nil
}

// listPicks returns every Pick with its Entry's name, ordered by Pick number.
func (s *Store) listPicks(ctx context.Context) ([]model.ListPick, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT c.name, p.picked_at, p.pick_number
		FROM picks p JOIN entries c ON c.id = p.entry_id
		ORDER BY p.pick_number`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.ListPick
	for rows.Next() {
		var p model.ListPick
		if err := rows.Scan(&p.EntryName, &p.PickedAt, &p.PickNumber); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if out == nil {
		out = []model.ListPick{}
	}
	return out, rows.Err()
}

// ApplyList merges an imported List into the database in one transaction:
// either all of it lands or none of it does. The List name is set only when
// the List carries one. Entries are matched by name; new ones are added, and
// existing ones get Pick count and Excluded only where the List supplies them.
// Tags are replaced when supplied, created as needed.
func (s *Store) ApplyList(ctx context.Context, l model.List) (added, updated int, err error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	if strings.TrimSpace(l.Name) != "" {
		if err := setListName(ctx, tx, l.Name); err != nil {
			return 0, 0, err
		}
	}

	for _, ci := range l.Entries {
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
			return 0, 0, err
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
				return 0, 0, err
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
					return 0, 0, err
				}
			}
			updated++
		}

		if len(ci.Tags) > 0 {
			if err := s.SetEntryTags(ctx, tx, id, ci.Tags); err != nil {
				return 0, 0, err
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

// Reset clears all Picks and sets every Pick count back to zero, in a
// transaction serialized against Draws and Undo.
func (s *Store) Reset(ctx context.Context) error {
	tx, err := s.beginPicksTx(ctx)
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
