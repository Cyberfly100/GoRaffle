package db

import (
	"context"
	"database/sql"

	"github.com/goraffle/raffle/internal/model"
)

// PoolEntry is one Entry of the Eligible pool as a Draw sees it.
type PoolEntry struct {
	ID        int
	Name      string
	PickCount int
}

// PicksTx is a transaction that holds the Picks lock. Every query a Draw needs
// runs through it, so a Draw cannot read outside its own snapshot.
type PicksTx struct {
	tx *sql.Tx
}

// InPicksTx runs fn inside one transaction that is serialized against every
// other transaction that mutates Picks (Draw, Undo, Reset). The transaction
// commits if fn returns nil and rolls back otherwise.
func (s *Store) InPicksTx(ctx context.Context, fn func(*PicksTx) error) error {
	tx, err := s.beginPicksTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(&PicksTx{tx: tx}); err != nil {
		return err
	}
	return tx.Commit()
}

// picksLockKey identifies the transaction-scoped advisory lock that every
// transaction mutating Picks takes first.
// The value is arbitrary but must stay the same for all of them.
const picksLockKey = 727001

// beginPicksTx opens a transaction and takes the Picks lock, which is held
// until the transaction commits or rolls back.
func (s *Store) beginPicksTx(ctx context.Context) (*sql.Tx, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, picksLockKey); err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}

// EligiblePool returns the Eligible pool under the Tag filter.
func (p *PicksTx) EligiblePool(ctx context.Context, filter model.TagFilter) ([]PoolEntry, error) {
	return eligiblePool(ctx, p.tx, filter)
}

// EntryWithTags returns one Entry with its Tags.
func (p *PicksTx) EntryWithTags(ctx context.Context, id int) (model.Entry, error) {
	return getEntry(ctx, p.tx, id)
}

// RecordPick inserts a Pick for the Entry and increments its Pick count.
// The Pick number is one more than the current highest.
// Returns the new Pick count, the Pick number and the Pick id.
func (p *PicksTx) RecordPick(ctx context.Context, entryID int) (count, pickNumber, pickID int, err error) {
	return recordPick(ctx, p.tx, entryID)
}

func eligiblePool(ctx context.Context, q querier, filter model.TagFilter) ([]PoolEntry, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT c.id, c.name, c.pick_count
		FROM entries c
		WHERE c.excluded = FALSE`+eligibleWhere+`
		ORDER BY c.id`, toNullArray(filter.MustHave), toNullArray(filter.AnyOf))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PoolEntry
	for rows.Next() {
		var e PoolEntry
		if err := rows.Scan(&e.ID, &e.Name, &e.PickCount); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func recordPick(ctx context.Context, q querier, entryID int) (int, int, int, error) {
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
