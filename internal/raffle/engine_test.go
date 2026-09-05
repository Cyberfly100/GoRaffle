package raffle

import (
	"math/rand/v2"
	"reflect"
	"sort"
	"testing"
)

func fixedRand() *rand.Rand {
	var seed [32]byte
	for i := range seed {
		seed[i] = byte(i)
	}
	return rand.New(rand.NewChaCha8(seed))
}

func TestChooseMinCount(t *testing.T) {
	tests := []struct {
		name    string
		board   map[int]int
		wantErr bool
	}{
		{"empty pool errors", map[int]int{}, true},
		{"single contender", map[int]int{1: 5}, false},
		{"multiple min-count ties", map[int]int{1: 3, 2: 3, 3: 5, 4: 3}, false},
		{"all tied", map[int]int{1: 0, 2: 0, 3: 0}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := chooseMinCount(tt.board, fixedRand())
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if _, ok := tt.board[got]; !ok {
				t.Fatalf("winner %d not in board", got)
			}
		})
	}
}

// chooseMinCount must only ever return ids tied at the minimum count.
func TestChooseMinCountRestrictedToMin(t *testing.T) {
	board := map[int]int{1: 4, 2: 1, 3: 1, 4: 9, 5: 2}
	for i := 0; i < 200; i++ {
		winner, err := chooseMinCount(board, fixedRand())
		if err != nil {
			t.Fatal(err)
		}
		if winner != 2 && winner != 3 {
			t.Fatalf("winner %d should have been 2 or 3 (the min-count ties)", winner)
		}
	}
}

// Multiple draws exercise both tie siblings when the same seed would otherwise
// always pick one. Different PCG states must cover all tied ids over many runs.
func TestChooseMinCountCoversAllTies(t *testing.T) {
	board := map[int]int{10: 0, 11: 0, 12: 0}
	seen := map[int]bool{}
	for i := 0; i < 300; i++ {
		w, err := chooseMinCount(board, fixedRand())
		if err != nil {
			t.Fatal(err)
		}
		seen[w] = true
		if len(seen) == len(board) {
			return // all three seen
		}
	}
	t.Fatalf("only saw winners %v; expected all of %v", sortedKeys(seen), []int{10, 11, 12})
}

func sortedKeys(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

func TestChooseMinCountUpToDateChecked(t *testing.T) {
	// Sanity: maps with equal minimum counts must produce a valid member each call.
	board := map[int]int{}
	board = map[int]int{1: 2, 2: 2, 3: 2}
	for i := 0; i < 50; i++ {
		w, err := chooseMinCount(board, fixedRand())
		if err != nil || !reflect.ValueOf(w).IsValid() {
			t.Fatalf("invalid winner %d (err=%v)", w, err)
		}
	}
}
