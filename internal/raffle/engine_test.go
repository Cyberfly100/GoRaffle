package raffle

import (
	"reflect"
	"testing"

	"github.com/goraffle/raffle/internal/db"
)

func TestPotOf(t *testing.T) {
	e := func(id, count int) db.PoolEntry { return db.PoolEntry{ID: id, PickCount: count} }
	tests := []struct {
		name string
		pool []db.PoolEntry
		want []int
	}{
		{"empty pool has empty Pot", nil, nil},
		{"single Entry", []db.PoolEntry{e(1, 5)}, []int{1}},
		{"fewest Picks only", []db.PoolEntry{e(1, 4), e(2, 1), e(3, 1), e(4, 9), e(5, 2)}, []int{2, 3}},
		{"later lower count replaces earlier", []db.PoolEntry{e(1, 3), e(2, 3), e(3, 0)}, []int{3}},
		{"all tied", []db.PoolEntry{e(1, 0), e(2, 0), e(3, 0)}, []int{1, 2, 3}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []int
			for _, p := range potOf(tt.pool) {
				got = append(got, p.ID)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Pot = %v, want %v", got, tt.want)
			}
		})
	}
}
