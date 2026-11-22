package handler

import (
	"context"
	"testing"
	"time"
)

// pickSet stands in for the Store: the Picks that still exist.
type pickSet map[int]bool

func (p pickSet) stands(_ context.Context, id int) (bool, error) { return p[id], nil }

// A Reveal remembered only after its Pick was already undone (the Draw's
// remember lost the race against the Undo) is never replayed.
func TestRevealRememberedAfterItsPickIsGoneIsNotReplayed(t *testing.T) {
	picks := pickSet{}
	var r revealReplay
	r.setWindow(time.Minute)
	r.remember(7, []byte("reveal 7")) // Pick 7 was drawn and undone already
	if msg := r.replay(context.Background(), picks.stands); msg != nil {
		t.Errorf("replayed %q for a Pick that is gone", msg)
	}
}

// A late remember of an older Draw never replaces the newer Reveal.
func TestOlderRevealNeverReplacesNewer(t *testing.T) {
	picks := pickSet{7: true, 8: true}
	var r revealReplay
	r.setWindow(time.Minute)
	r.remember(8, []byte("reveal 8"))
	r.remember(7, []byte("reveal 7"))
	if msg := r.replay(context.Background(), picks.stands); string(msg) != "reveal 8" {
		t.Errorf("replayed %q, want the newest Reveal", msg)
	}
}

// Outside the window nothing replays, even if the Pick stands.
func TestNoReplayAfterWindow(t *testing.T) {
	picks := pickSet{1: true}
	var r revealReplay
	r.setWindow(time.Millisecond)
	r.remember(1, []byte("reveal 1"))
	time.Sleep(5 * time.Millisecond)
	if msg := r.replay(context.Background(), picks.stands); msg != nil {
		t.Errorf("replayed %q after the window", msg)
	}
}
