package handler

import (
	"sync"
	"time"
)

// defaultRevealReplayWindow is how long after a Draw a newly connected
// browser is still sent its Reveal: the Reel's Reveal (Suspense 2s, landing
// ~1.8s, settle 0.42s; see the constants in static/js/reel.js) plus a margin.
// After it, the browser's normal page render already shows the result.
const defaultRevealReplayWindow = 6 * time.Second

// lastReveal remembers the most recent Reveal event and when it was sent, so
// a browser that (re)connects during it can play it too.
type lastReveal struct {
	mu     sync.Mutex
	msg    []byte
	sentAt time.Time
	window time.Duration
}

func (l *lastReveal) remember(msg []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.msg, l.sentAt = msg, time.Now()
}

// forget drops the remembered Reveal (its Pick was undone or reset).
func (l *lastReveal) forget() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.msg = nil
}

// replay returns the Reveal event to send a browser connecting now, or nil.
func (l *lastReveal) replay() []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.msg == nil || time.Since(l.sentAt) > l.window {
		return nil
	}
	return l.msg
}

// SetRevealReplayWindow changes how long after a Draw a connecting browser
// is still sent its Reveal.
func (s *Server) SetRevealReplayWindow(d time.Duration) {
	s.lastReveal.mu.Lock()
	defer s.lastReveal.mu.Unlock()
	s.lastReveal.window = d
}
