package db_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/goraffle/raffle/internal/db"
	"github.com/goraffle/raffle/internal/model"
)

// The Import's serialization guarantee, proven rather than assumed: an
// Import raced against concurrent Draws, Undoes and Resets ends on exactly
// the state SOME serialized order of those same operations produces —
// never a half-replaced List, never Pick numbers out of sequence, never
// Pick counts that disagree with the History.
//
// The set of serialized outcomes is computed here by a model of the domain
// rules (GLOSSARY.md), written independently of the store: an Import
// replaces the List with the file's; a Draw picks any Entry of the Pot; an
// Undo takes back the most recent Pick; a Reset clears the Picks. The
// raced operations run exactly as a host or script runs them, and only
// observable state is asserted.

// racedOp is one kind of operation the test races.
type racedOp int

const (
	opImport racedOp = iota
	opDraw
	opUndo
	opReset
	opCount
)

// racedOps counts how many of each kind run in the race.
type racedOps [opCount]int

func (r racedOps) done() bool {
	for _, n := range r {
		if n > 0 {
			return false
		}
	}
	return true
}

// outcome is one observable state a serialized run can be in: the List
// name, every Entry's Pick count, and the History's winners oldest first.
type outcome struct {
	listName string
	counts   map[string]int
	history  []string
}

func (o outcome) clone() outcome {
	counts := make(map[string]int, len(o.counts))
	for name, c := range o.counts {
		counts[name] = c
	}
	return outcome{listName: o.listName, counts: counts, history: append([]string(nil), o.history...)}
}

// key is the outcome's canonical form, for comparing and memoizing states.
func (o outcome) key() string {
	names := make([]string, 0, len(o.counts))
	for name := range o.counts {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names)+2)
	parts = append(parts, "list="+o.listName)
	for _, name := range names {
		parts = append(parts, name+"="+strconv.Itoa(o.counts[name]))
	}
	parts = append(parts, "history="+strings.Join(o.history, ","))
	return strings.Join(parts, "|")
}

func (o outcome) String() string {
	names := make([]string, 0, len(o.counts))
	for name := range o.counts {
		names = append(names, name)
	}
	sort.Strings(names)
	pairs := make([]string, 0, len(names))
	for _, name := range names {
		pairs = append(pairs, fmt.Sprintf("%s:%d", name, o.counts[name]))
	}
	return fmt.Sprintf("list %q, counts {%s}, history [%s]",
		o.listName, strings.Join(pairs, " "), strings.Join(o.history, " "))
}

// applyOp returns every state one operation can produce from o, by the
// domain rules. A Draw can produce several: its winner is any Entry of
// the Pot.
func applyOp(op racedOp, o, imported outcome) []outcome {
	switch op {
	case opImport:
		// An Import replaces the List's name, Entries and History with
		// the file's. It never merges.
		return []outcome{imported.clone()}
	case opReset:
		// A Reset clears all Picks and sets every Pick count back to
		// zero. The Entries and the List name stay.
		next := o.clone()
		for name := range next.counts {
			next.counts[name] = 0
		}
		next.history = nil
		return []outcome{next}
	case opUndo:
		// An Undo takes back the most recent Pick and lowers that
		// Entry's Pick count, floored at zero. On an empty History
		// there is nothing to take back.
		if len(o.history) == 0 {
			return []outcome{o.clone()}
		}
		next := o.clone()
		winner := next.history[len(next.history)-1]
		next.history = next.history[:len(next.history)-1]
		next.counts[winner]--
		if next.counts[winner] < 0 {
			next.counts[winner] = 0
		}
		return []outcome{next}
	case opDraw:
		// The Pot holds the Entries with the fewest Picks; a Draw's
		// winner is any one of them.
		min := -1
		for _, c := range o.counts {
			if min < 0 || c < min {
				min = c
			}
		}
		var pot []string
		for name, c := range o.counts {
			if c == min {
				pot = append(pot, name)
			}
		}
		sort.Strings(pot)
		out := make([]outcome, 0, len(pot))
		for _, winner := range pot {
			next := o.clone()
			next.counts[winner]++
			next.history = append(next.history, winner)
			out = append(out, next)
		}
		return out
	}
	panic("unknown operation")
}

// serializedOutcomes computes every state the raced operations can end on
// when they run one after the other, in any order, with every legal Draw.
func serializedOutcomes(start, imported outcome, raced racedOps) map[string]outcome {
	type memoKey struct {
		remaining racedOps
		state     string
	}
	finals := map[string]outcome{}
	visited := map[memoKey]bool{}
	var walk func(remaining racedOps, o outcome)
	walk = func(remaining racedOps, o outcome) {
		if remaining.done() {
			finals[o.key()] = o
			return
		}
		k := memoKey{remaining: remaining, state: o.key()}
		if visited[k] {
			return
		}
		visited[k] = true
		for op := racedOp(0); op < opCount; op++ {
			if remaining[op] == 0 {
				continue
			}
			remaining[op]--
			for _, next := range applyOp(op, o, imported) {
				walk(remaining, next)
			}
			remaining[op]++
		}
	}
	walk(raced, start)
	return finals
}

// An Import raced against concurrent Draws, Undoes and Resets behaves as
// if the operations had run one after the other: Pick numbers stay unique
// and consecutive, and the List, Pick counts and History afterwards are
// exactly one serialized order's outcome — never a half-replaced List.
func TestConcurrentImportsDrawsUndoesAndResets(t *testing.T) {
	store, engine := setup(t)
	ctx := context.Background()
	addEntries(t, store, "a", "b", "c")

	// The List file every raced Import restores.
	file := model.List{
		Name:    "Imported",
		Entries: []model.ListEntry{{Name: "x"}, {Name: "y"}, {Name: "z"}},
		Picks: []model.ListPick{
			{EntryName: "x", PickedAt: time.Unix(1000, 0), PickNumber: 1},
			{EntryName: "y", PickedAt: time.Unix(2000, 0), PickNumber: 2},
			{EntryName: "z", PickedAt: time.Unix(3000, 0), PickNumber: 3},
		},
	}
	// What that file restores, by the Import rules: Entries x, y and z,
	// each with the one Pick the file gives it (no Pick count is
	// supplied, so each count is the Entry's Picks in the file), and
	// the file's Picks as the History.
	imported := outcome{
		listName: "Imported",
		counts:   map[string]int{"x": 1, "y": 1, "z": 1},
		history:  []string{"x", "y", "z"},
	}
	// The List the race starts from: Entries a, b and c with no Picks,
	// under its initial name.
	start := outcome{
		listName: "Entries",
		counts:   map[string]int{"a": 0, "b": 0, "c": 0},
	}

	// The race: 3 Imports, 5 Draws, 2 Undos and 2 Resets, all at once.
	// An Undo on an empty History is a no-op (ErrNotFound), exactly as
	// in a serialized run.
	raced := racedOps{opImport: 3, opDraw: 5, opUndo: 2, opReset: 2}
	var kinds []racedOp
	for op := racedOp(0); op < opCount; op++ {
		for i := 0; i < raced[op]; i++ {
			kinds = append(kinds, op)
		}
	}
	concurrently(t, len(kinds), func(i int) error {
		switch kinds[i] {
		case opImport:
			_, err := store.ImportList(ctx, file, db.ImportOptions{})
			return err
		case opDraw:
			_, err := engine.Draw(ctx, model.TagFilter{})
			return err
		case opUndo:
			if _, err := store.UndoLastPick(ctx); err != nil && !errors.Is(err, db.ErrNotFound) {
				return err
			}
			return nil
		case opReset:
			return store.Reset(ctx)
		}
		return nil
	})

	// The List afterwards is exactly one operation's Entries — the
	// imported List's or the original List's, never a half-replaced mix.
	entries, err := store.ListEntries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name)
	}
	sort.Strings(names)
	drawnList := []string{"a", "b", "c"}
	importedList := []string{"x", "y", "z"}
	if !reflect.DeepEqual(names, drawnList) && !reflect.DeepEqual(names, importedList) {
		t.Errorf("entries after the race = %v, want exactly %v or %v: the List was half-replaced",
			names, drawnList, importedList)
	}

	// Pick numbers stay unique and consecutive (historyInOrder fails
	// the test otherwise) — and the History's winners oldest first.
	picks := historyInOrder(t, store)

	// The whole observable state — List name, Entries with their Pick
	// counts, History — matches the outcome of SOME serialized order.
	listName, err := store.GetListName(ctx)
	if err != nil {
		t.Fatal(err)
	}
	final := outcome{listName: listName, counts: map[string]int{}, history: []string{}}
	for _, e := range entries {
		final.counts[e.Name] = e.PickCount
	}
	for _, p := range picks {
		final.history = append(final.history, p.EntryName)
	}
	possible := serializedOutcomes(start, imported, raced)
	if _, ok := possible[final.key()]; !ok {
		var examples []string
		for _, o := range possible {
			examples = append(examples, o.String())
			if len(examples) == 6 {
				break
			}
		}
		t.Errorf("after the race the List is\n  %s\n"+
			"which no serialized order of the 3 Imports, 5 Draws, 2 Undos and 2 Resets produces "+
			"(one of %d outcomes, e.g.\n  %s)",
			final, len(possible), strings.Join(examples, "\n  "))
	}
}
