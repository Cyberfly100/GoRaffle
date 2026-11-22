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
// Its JSON encoding is both the HTTP Draw response and the websocket reveal
// event, so every browser plays the same Reveal from either.
type Reveal struct {
	Winner     model.Entry `json:"winner"`
	PickID     int         `json:"pick_id"`
	PickNumber int         `json:"pick_number"`
	model.TagFilter
	EligiblePool  int      `json:"eligible_pool"`
	SuspenseNames []string `json:"suspense_names"`
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

// Draw picks a random winner from the Pot of the Eligible pool under the Tag
// filter, records the Pick, and returns the Reveal. Everything happens in one
// transaction serialized against other Draws, Undo and Reset. With an empty
// Eligible pool it returns ErrNoEligible and changes nothing.
func (e *Engine) Draw(ctx context.Context, filter model.TagFilter) (*Reveal, error) {
	var reveal *Reveal
	err := e.store.InPicksTx(ctx, func(tx *db.PicksTx) error {
		pool, err := tx.EligiblePool(ctx, filter)
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

		pick, err := tx.RecordPick(ctx, winner.ID)
		if err != nil {
			return err
		}
		entry, err := tx.EntryWithTags(ctx, winner.ID)
		if err != nil {
			return err
		}
		entry.PickCount = pick.PickCount

		reveal = &Reveal{
			Winner:        entry,
			PickID:        pick.PickID,
			PickNumber:    pick.PickNumber,
			TagFilter:     filter,
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

// MarkedEntry is an Entry marked with where it stands for the next Draw under
// a Tag filter.
type MarkedEntry struct {
	model.Entry
	Eligible bool // in the Eligible pool
	InPot    bool // in the Pot: could win the next Draw
}

// MarkEntries returns every Entry on the List, marked by the same Eligible pool
// and Pot a Draw under the Tag filter would use, read from one snapshot.
func (e *Engine) MarkEntries(ctx context.Context, filter model.TagFilter) ([]MarkedEntry, error) {
	entries, pool, err := e.store.EntriesAndPool(ctx, filter)
	if err != nil {
		return nil, err
	}
	eligible := make(map[int]bool, len(pool))
	for _, p := range pool {
		eligible[p.ID] = true
	}
	pot := make(map[int]bool)
	for _, p := range potOf(pool) {
		pot[p.ID] = true
	}
	out := make([]MarkedEntry, len(entries))
	for i, entry := range entries {
		out[i] = MarkedEntry{Entry: entry, Eligible: eligible[entry.ID], InPot: pot[entry.ID]}
	}
	return out, nil
}
