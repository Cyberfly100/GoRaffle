package raffle

import (
	"context"
	"errors"
	"math/rand/v2"

	"github.com/goraffle/raffle/internal/db"
	"github.com/goraffle/raffle/internal/model"
)

var ErrNoEligible = errors.New("no eligible entries")

// Engine performs draws against a Store. It is safe for concurrent use.
type Engine struct {
	store *db.Store
	rand  *rand.Rand
}

func NewEngine(store *db.Store) *Engine {
	return &Engine{store: store, rand: rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))}
}

// Draw picks a random winner among the eligible entries with the minimum
// pick_count. mustHave / anyOf apply the tag filter (both empty = no filter).
// All DB mutations happen in a single transaction.
func (e *Engine) Draw(ctx context.Context, mustHave, anyOf []string) (*model.DrawResult, error) {
	tx, err := e.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	ids, err := e.store.EligiblePool(ctx, mustHave, anyOf)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, ErrNoEligible
	}

	// Find the minimum pick_count among eligible entries (ties resolved randomly).
	leaderboard, err := e.store.DrawLeaderboard(ctx, ids)
	if err != nil {
		return nil, err
	}
	winnerID, err := chooseMinCount(leaderboard, e.rand)
	if err != nil {
		return nil, err
	}

	newCount, pickNumber, pickID, err := e.store.RecordPick(ctx, tx, winnerID)
	if err != nil {
		return nil, err
	}

	// Grab the winner with tags while we still hold the transaction.
	winner, err := e.store.GetEntry(ctx, winnerID)
	if err != nil {
		return nil, err
	}
	winner.PickCount = newCount

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &model.DrawResult{
		Winner:       winner,
		PickID:       pickID,
		PickNumber:   pickNumber,
		MustHave:     mustHave,
		AnyOf:        anyOf,
		EligiblePool: len(ids),
	}, nil
}

// chooseMinCount picks a random id among those with the minimum count.
// The order of the tied candidates is random because leaderboard is a map.
func chooseMinCount(leaderboard map[int]int, r *rand.Rand) (int, error) {
	if len(leaderboard) == 0 {
		return 0, ErrNoEligible
	}
	minCount := -1
	for _, count := range leaderboard {
		if minCount == -1 || count < minCount {
			minCount = count
		}
	}
	var tied []int
	for id, count := range leaderboard {
		if count == minCount {
			tied = append(tied, id)
		}
	}
	return tied[r.IntN(len(tied))], nil
}
