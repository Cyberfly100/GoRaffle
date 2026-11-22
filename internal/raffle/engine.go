// Package raffle is the Draw module: the single home of a fair Draw.
package raffle

import (
	"context"
	"errors"
	"math/rand/v2"
	"sync"

	"github.com/goraffle/raffle/internal/db"
	"github.com/goraffle/raffle/internal/model"
)

var ErrNoEligible = errors.New("no eligible entries")

// SuspenseNameCount is how many names a Reveal's Suspense flashes on the Reel.
const SuspenseNameCount = 30

// Reveal is the one result of a Draw: everything every browser is shown for it.
// Its JSON encoding is the HTTP draw response and the winner event; the
// Suspense names are not part of it and travel only in the suspense event.
type Reveal struct {
	Winner        model.Entry `json:"winner"`
	PickID        int         `json:"pick_id"`
	PickNumber    int         `json:"pick_number"`
	MustHave      []string    `json:"must_have,omitempty"`
	AnyOf         []string    `json:"any_of,omitempty"`
	EligiblePool  int         `json:"eligible_pool"`
	SuspenseNames []string    `json:"-"`
}

// Engine performs Draws against a Store. It is safe for concurrent use.
type Engine struct {
	store *db.Store
	mu    sync.Mutex // guards rand
	rand  *rand.Rand
}

func NewEngine(store *db.Store) *Engine {
	return &Engine{store: store, rand: rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))}
}

// Draw picks a random winner from the Pot of the Eligible pool under the tag
// filter (mustHave = ALL required, anyOf = at least one; both empty = no
// filter), records the Pick, and returns the Reveal. Everything happens in one
// transaction serialized against other Draws, Undo and Reset. With an empty
// Eligible pool it returns ErrNoEligible and changes nothing.
func (e *Engine) Draw(ctx context.Context, mustHave, anyOf []string) (*Reveal, error) {
	var reveal *Reveal
	err := e.store.InPicksTx(ctx, func(tx *db.PicksTx) error {
		pool, err := tx.EligiblePool(ctx, mustHave, anyOf)
		if err != nil {
			return err
		}
		pot := potOf(pool)
		if len(pot) == 0 {
			return ErrNoEligible
		}

		e.mu.Lock()
		winner := pot[e.rand.IntN(len(pot))]
		suspense := make([]string, SuspenseNameCount)
		for i := range suspense {
			suspense[i] = pot[e.rand.IntN(len(pot))].Name
		}
		e.mu.Unlock()

		count, pickNumber, pickID, err := tx.RecordPick(ctx, winner.ID)
		if err != nil {
			return err
		}
		entry, err := tx.EntryWithTags(ctx, winner.ID)
		if err != nil {
			return err
		}
		entry.PickCount = count

		reveal = &Reveal{
			Winner:        entry,
			PickID:        pickID,
			PickNumber:    pickNumber,
			MustHave:      mustHave,
			AnyOf:         anyOf,
			EligiblePool:  len(pool),
			SuspenseNames: suspense,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return reveal, nil
}

// potOf returns the Entries of the Eligible pool with the fewest Picks.
func potOf(pool []db.PoolEntry) []db.PoolEntry {
	var pot []db.PoolEntry
	for _, e := range pool {
		switch {
		case len(pot) == 0 || e.PickCount < pot[0].PickCount:
			pot = []db.PoolEntry{e}
		case e.PickCount == pot[0].PickCount:
			pot = append(pot, e)
		}
	}
	return pot
}
