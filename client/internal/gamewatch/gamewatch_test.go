package gamewatch

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var t0 = time.Unix(1_700_000_000, 0)

func at(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }

func newW() *Watcher { return New(10*time.Minute, 2*time.Minute, t0) }

func TestNothingHappensWhileTheGameRuns(t *testing.T) {
	w := newW()
	for m := 0; m < 120; m++ {
		assert.Equal(t, None, w.Tick(at(m), true))
	}
}

func TestAsksAfterTheGameHasBeenAbsentForTheWholeWindow(t *testing.T) {
	w := newW()
	assert.Equal(t, None, w.Tick(at(9), false))
	assert.Equal(t, Ask, w.Tick(at(10), false))
	assert.Equal(t, None, w.Tick(at(11), false), "asked once, not on every tick")
}

func TestDisconnectsWhenNobodyAnswers(t *testing.T) {
	w := newW()
	assert.Equal(t, Ask, w.Tick(at(10), false))
	assert.Equal(t, None, w.Tick(at(11), false))
	assert.Equal(t, Disconnect, w.Tick(at(12), false))
}

func TestStartingTheGameCancelsTheQuestion(t *testing.T) {
	w := newW()
	assert.Equal(t, Ask, w.Tick(at(10), false))
	assert.Equal(t, Cancel, w.Tick(at(11), true))
	assert.Equal(t, None, w.Tick(at(30), true))
	assert.Equal(t, None, w.Tick(at(39), false), "the absence counts from when the game was last seen")
	assert.Equal(t, Ask, w.Tick(at(41), false))
}

func TestKeepingTheConnectionStartsTheWindowAgain(t *testing.T) {
	w := newW()
	assert.Equal(t, Ask, w.Tick(at(10), false))
	w.Keep(at(10))
	assert.Equal(t, None, w.Tick(at(19), false))
	assert.Equal(t, Ask, w.Tick(at(20), false))
}

func TestRunningMatchesTheProgramNameNotAPath(t *testing.T) {
	comms := []string{"/usr/libexec/x", "/Applications/Some Launcher.app/Contents/MacOS/Some Launcher", "/Applications/G.app/Contents/MacOS/The Game "}
	assert.True(t, Running([]string{"The Game"}, comms))
	assert.False(t, Running([]string{"Some"}, comms), "a part of a name is not the program")
	assert.False(t, Running(nil, comms))
}
