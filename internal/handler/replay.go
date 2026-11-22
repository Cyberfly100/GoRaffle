package handler

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// defaultRevealReplayWindow is how long after a Draw a newly connected
// browser is still sent its Reveal: the Reel's Reveal (Suspense 2s, landing
// ~1.8s, settle 0.42s; see the constants in static/js/reel.js) plus a margin.
// After it, the browser's normal page render already shows the result.
const defaultRevealReplayWindow = 6 * time.Second

// revealReplay remembers the most recent Reveal event, its Pick and when it
// was sent, so a browser that (re)connects during it can play it too.
//
// It owns whether a Reveal is still replayable: replay asks the Store whether
// the Pick still stands, so no Pick-removing path (Undo, Reset, Clear,
// deleting the winning Entry, replacing the List) has to remember to forget
// it, and a remember that lands after the Pick is already gone is harmless.
type revealReplay struct {
	mu     sync.Mutex
	pickID int
	msg    []byte
	sentAt time.Time
	window time.Duration
}

// remember records the Reveal event of a Pick. A late remember of an older
// Pick (concurrent Draws) never replaces a newer one: Pick ids only grow.
func (r *revealReplay) remember(pickID int, msg []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.msg != nil && pickID < r.pickID {
		return
	}
	r.pickID, r.msg, r.sentAt = pickID, msg, time.Now()
}

// setWindow changes how long after a Draw a connecting browser is still sent
// its Reveal.
func (r *revealReplay) setWindow(d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.window = d
}

// replay returns the Reveal event to send a browser connecting now, or nil:
// nil outside the window, and nil once its Pick no longer stands.
func (r *revealReplay) replay(ctx context.Context, stands func(context.Context, int) (bool, error)) []byte {
	r.mu.Lock()
	pickID, msg := r.pickID, r.msg
	live := msg != nil && time.Since(r.sentAt) <= r.window
	r.mu.Unlock()
	if !live {
		return nil
	}
	ok, err := stands(ctx, pickID)
	if err != nil {
		slog.Warn("check replayed Pick", "pick_id", pickID, "err", err)
		return nil
	}
	if !ok {
		return nil
	}
	return msg
}

// SetRevealReplayWindow changes how long after a Draw a connecting browser
// is still sent its Reveal.
func (s *Server) SetRevealReplayWindow(d time.Duration) {
	s.lastReveal.setWindow(d)
}
