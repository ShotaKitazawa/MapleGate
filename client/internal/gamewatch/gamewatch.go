// Package gamewatch decides when a tunnel nobody is using should be offered for disconnection.
package gamewatch

import (
	"path/filepath"
	"strings"
	"time"
)

type Action int

const (
	None Action = iota
	Ask
	Cancel
	Disconnect
)

// Watcher is a state machine over a clock and a yes/no "is the game running"; it does no I/O.
type Watcher struct {
	Absent, Answer time.Duration

	lastSeen, askedAt time.Time
	asking            bool
}

func New(absent, answer time.Duration, now time.Time) *Watcher {
	return &Watcher{Absent: absent, Answer: answer, lastSeen: now}
}

func (w *Watcher) Tick(now time.Time, running bool) Action {
	if running {
		w.lastSeen = now
		if w.asking {
			w.asking = false
			return Cancel
		}
		return None
	}
	if w.asking {
		if now.Sub(w.askedAt) >= w.Answer {
			w.asking = false
			w.lastSeen = now
			return Disconnect
		}
		return None
	}
	if now.Sub(w.lastSeen) >= w.Absent {
		w.asking, w.askedAt = true, now
		return Ask
	}
	return None
}

// Keep is the person's answer to stay connected.
func (w *Watcher) Keep(now time.Time) {
	w.asking = false
	w.lastSeen = now
}

// Running reports whether one of names is among the executables in comms. Names are compared whole: a launcher
// whose name starts with the game's must not keep a tunnel nobody uses.
func Running(names, comms []string) bool {
	for _, c := range comms {
		base := filepath.Base(strings.TrimSpace(c))
		for _, n := range names {
			if base == n {
				return true
			}
		}
	}
	return false
}
