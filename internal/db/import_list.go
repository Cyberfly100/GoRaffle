package db

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/goraffle/raffle/internal/model"
)

func boolOr(b *bool, def bool) bool {
	if b != nil {
		return *b
	}
	return def
}

// ImportOptions are the Import's only options: a direct request replaces
// at once, with no confirmation flag.
type ImportOptions struct {
	// SkipMissingPicks drops the Picks that name Entries the file doesn't
	// contain, instead of stopping the Import.
	SkipMissingPicks bool
	// RemoveUnusedTags removes Tags that no imported Entry carries.
	// Off by default: a mistaken Import keeps the old Tags and can be
	// corrected by importing again.
	RemoveUnusedTags bool
}

// ImportResult reports what an Import replaced.
type ImportResult struct {
	ListName string // the effective List name ("Entries" when the file has none)
	Entries  int    // Entries created
	Picks    int    // Picks recorded as the new History
}

// MissingEntriesError is the Import's distinct error for a file whose
// History names Entries the file doesn't contain. Nothing has changed.
type MissingEntriesError struct {
	Names []string // the missing names, as they appear in the file, deduped case-insensitively
	Picks int      // how many Picks name a missing Entry
}

func (e *MissingEntriesError) Error() string {
	return fmt.Sprintf("the History names %d Picks of Entries that are not in the file: %s",
		e.Picks, strings.Join(e.Names, ", "))
}

// ImportList replaces the List name, Entries and History with what the
// file's List says, in one transaction that holds the Picks lock, so it
// never interleaves with a Draw, Undo or Reset. Nothing merges: the result
// depends only on the file, and it is all or nothing — on any error
// nothing changes.
//
// Picks match Entries by name, case-insensitively, against the file's
// Entries. They are sorted by the file's Pick number, ties broken by time,
// and renumbered 1 to n. An Entry's Pick count from the file wins; an
// Entry with no count gets the number of its Picks in the file.
func (s *Store) ImportList(ctx context.Context, l model.List, opts ImportOptions) (ImportResult, error) {
	// The file's Entries by name, case-insensitive.
	byKey := make(map[string]int, len(l.Entries))
	for i, e := range l.Entries {
		byKey[strings.ToLower(strings.TrimSpace(e.Name))] = i
	}

	// The file's order of the Picks: Pick number first, ties by time.
	picks := make([]model.ListPick, len(l.Picks))
	copy(picks, l.Picks)
	sort.SliceStable(picks, func(i, j int) bool {
		if picks[i].PickNumber != picks[j].PickNumber {
			return picks[i].PickNumber < picks[j].PickNumber
		}
		return picks[i].PickedAt.Before(picks[j].PickedAt)
	})

	// Split the Picks into those the file's Entries account for and the rest.
	counts := make([]int, len(l.Entries)) // each Entry's Picks in the file
	var kept []model.ListPick
	var missing MissingEntriesError
	missingSeen := make(map[string]bool)
	for _, p := range picks {
		idx, ok := byKey[strings.ToLower(strings.TrimSpace(p.EntryName))]
		if !ok {
			missing.Picks++
			key := strings.ToLower(p.EntryName)
			if !missingSeen[key] {
				missingSeen[key] = true
				missing.Names = append(missing.Names, p.EntryName)
			}
			continue
		}
		counts[idx]++
		kept = append(kept, p)
	}
	if missing.Picks > 0 && !opts.SkipMissingPicks {
		sort.Strings(missing.Names)
		return ImportResult{}, &missing
	}

	name := strings.TrimSpace(l.Name)
	if name == "" {
		name = "Entries"
	}
	res := ImportResult{ListName: name, Entries: len(l.Entries), Picks: len(kept)}
	err := s.InPicksTx(ctx, func(tx *PicksTx) error {
		if err := setListName(ctx, tx.tx, name); err != nil {
			return err
		}
		// Removing the Entries removes their History and Tag links
		// (cascades), so the file's land on an empty List.
		if _, err := tx.tx.ExecContext(ctx, `DELETE FROM entries`); err != nil {
			return err
		}
		ids := make([]int, len(l.Entries))
		for i, e := range l.Entries {
			count := counts[i]
			if e.PickCount != nil {
				count = *e.PickCount
			}
			if err := tx.tx.QueryRowContext(ctx,
				`INSERT INTO entries (name, pick_count, excluded) VALUES ($1, $2, $3) RETURNING id`,
				strings.TrimSpace(e.Name), count, boolOr(e.Excluded, false)).Scan(&ids[i]); err != nil {
				return err
			}
			if err := s.SetEntryTags(ctx, tx.tx, ids[i], e.Tags); err != nil {
				return err
			}
		}
		for n, p := range kept {
			idx := byKey[strings.ToLower(strings.TrimSpace(p.EntryName))]
			if _, err := tx.tx.ExecContext(ctx,
				`INSERT INTO picks (entry_id, pick_number, picked_at) VALUES ($1, $2, $3)`,
				ids[idx], n+1, p.PickedAt); err != nil {
				return err
			}
		}
		if opts.RemoveUnusedTags {
			if _, err := tx.tx.ExecContext(ctx,
				`DELETE FROM tags WHERE id NOT IN (SELECT DISTINCT tag_id FROM entry_tags)`); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return ImportResult{}, err
	}
	return res, nil
}
