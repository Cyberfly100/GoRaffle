package raffle

import "time"

// RevealTiming is the one definition of how long a Reveal plays on the Reel.
// The page carries it to the browser (the Reel reads it from there), and the
// server's Reveal replay window is derived from it.
type RevealTiming struct {
	// SuspenseMS is how long names flash past before the landing starts.
	SuspenseMS int `json:"suspense_ms"`
	// LandingMS is the nominal landing: the Reel coasts to a stop on the
	// winner in about this long.
	LandingMS int `json:"landing_ms"`
	// LandingMaxMS bounds the landing, which otherwise depends on the
	// measured belt speed and cell size.
	LandingMaxMS int `json:"landing_max_ms"`
	// SettleMS is how long the landed winner rests before the Reel is idle.
	SettleMS int `json:"settle_ms"`
}

// Timing is the Reveal timing every browser plays.
var Timing = RevealTiming{SuspenseMS: 2000, LandingMS: 1800, LandingMaxMS: 3000, SettleMS: 420}

// Longest is the longest a Reveal can play, from the Suspense until the Reel
// is idle.
func (t RevealTiming) Longest() time.Duration {
	return time.Duration(t.SuspenseMS+t.LandingMaxMS+t.SettleMS) * time.Millisecond
}
