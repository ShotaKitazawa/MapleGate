package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ShotaKitazawa/MapleGate/client/internal/api"
	"github.com/ShotaKitazawa/MapleGate/client/internal/ipc"
	"github.com/ShotaKitazawa/MapleGate/client/internal/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type sessions struct {
	mu         sync.Mutex
	requestErr error
	pending    api.Session
	ended      chan api.Session
	closed     []string
}

func (s *sessions) Request(ctx context.Context, _ string, _ time.Duration) (api.Session, error) {
	if s.requestErr != nil {
		return s.pending, s.requestErr
	}
	return api.Session{ID: "s1", Status: "active", Config: &api.Config{Address: "10.66.0.2/32", ServerKey: "k", Endpoint: "1.2.3.4:51820", AllowedIPs: []string{"0.0.0.0/0"}}}, nil
}

func (s *sessions) Watch(ctx context.Context, _ string) (api.Session, error) {
	select {
	case <-ctx.Done():
		return api.Session{}, ctx.Err()
	case e := <-s.ended:
		return e, nil
	}
}

func (s *sessions) Close(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = append(s.closed, id)
	return nil
}

func (s *sessions) closedIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.closed...)
}

type daemon struct {
	mu     sync.Mutex
	upErr  error
	status ipc.Status
	ops    []string
}

func (d *daemon) Do(r ipc.Request) (ipc.Status, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ops = append(d.ops, r.Op)
	if r.Op == "up" {
		if d.upErr != nil {
			return ipc.Status{}, d.upErr
		}
		d.status = ipc.Status{State: "up", SessionID: r.SessionID}
	}
	if r.Op == "down" {
		d.status = ipc.Status{State: "down"}
	}
	return d.status, nil
}

func (d *daemon) set(s ipc.Status) { d.mu.Lock(); d.status = s; d.mu.Unlock() }
func (d *daemon) opList() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.ops...)
}

func rig() (*App, *sessions, *daemon) {
	s := &sessions{ended: make(chan api.Session, 1)}
	d := &daemon{}
	return &App{Sessions: s, Daemon: d, Wait: time.Minute, Poll: time.Millisecond}, s, d
}

func TestConnectEndsUpConnectedAfterTheDaemonIsAsked(t *testing.T) {
	a, _, d := rig()

	a.Connect()

	require.Eventually(t, func() bool { return a.State().Phase == Connected }, time.Second, time.Millisecond)
	assert.Contains(t, d.opList(), "up")
}

func TestARefusedRequestIsShownAndNothingIsStarted(t *testing.T) {
	a, _, d := rig()
	a.Sessions.(*sessions).requestErr = fmt.Errorf("wrapped: %w", &session.EndedError{ID: "s9", Status: "denied"})

	a.Connect()

	require.Eventually(t, func() bool { return a.State().Phase == Failed }, time.Second, time.Millisecond)
	assert.Equal(t, "The request was denied.", a.State().Text)
	assert.Contains(t, a.State().Detail, "s9", "the long form is kept for the log")
	assert.Empty(t, d.opList())
}

func TestWhenTheDaemonRefusesTheSessionIsClosed(t *testing.T) {
	a, s, d := rig()
	d.upErr = errors.New("not running")

	a.Connect()

	require.Eventually(t, func() bool { return a.State().Phase == Failed }, time.Second, time.Millisecond)
	assert.Equal(t, []string{"s1"}, s.closedIDs())
}

func TestDisconnectTakesDownTheTunnelAndClosesTheSession(t *testing.T) {
	a, s, d := rig()
	a.Connect()
	require.Eventually(t, func() bool { return a.State().Phase == Connected }, time.Second, time.Millisecond)

	a.Disconnect()

	assert.Equal(t, Idle, a.State().Phase)
	assert.Equal(t, []string{"s1"}, s.closedIDs())
	assert.Contains(t, d.opList(), "down")
}

func TestASessionThatEndsTakesTheTunnelDown(t *testing.T) {
	a, s, d := rig()
	a.Connect()
	require.Eventually(t, func() bool { return a.State().Phase == Connected }, time.Second, time.Millisecond)

	s.ended <- api.Session{ID: "s1", Status: "expired"}

	require.Eventually(t, func() bool { return a.State().Phase == Failed }, time.Second, time.Millisecond)
	assert.Equal(t, "The session reached its time limit. Connect again.", a.State().Text)
	assert.Contains(t, d.opList(), "down")
}

func TestADaemonThatDropsTheTunnelIsReportedAndTheSessionClosed(t *testing.T) {
	a, s, d := rig()
	a.Connect()
	require.Eventually(t, func() bool { return a.State().Phase == Connected }, time.Second, time.Millisecond)

	d.set(ipc.Status{State: "down", Error: "no route"})

	require.Eventually(t, func() bool { return a.State().Phase == Failed }, time.Second, time.Millisecond)
	assert.Equal(t, "The tunnel stopped.", a.State().Text)
	assert.Equal(t, "no route", a.State().Detail)
	assert.Equal(t, []string{"s1"}, s.closedIDs())
}

func TestARestartedAppAdoptsATunnelThatIsStillUp(t *testing.T) {
	a, s, d := rig()
	d.set(ipc.Status{State: "up", SessionID: "s9"})

	assert.True(t, a.Adopt())

	assert.Equal(t, Connected, a.State().Phase)
	a.Disconnect()
	assert.Equal(t, []string{"s9"}, s.closedIDs())
}

func TestThereIsNothingToAdoptWhenTheTunnelIsDown(t *testing.T) {
	a, _, _ := rig()

	assert.False(t, a.Adopt())
	assert.Equal(t, "", a.State().Phase)
}

func TestAConnectedLabelShowsHowLongTheTunnelHasBeenUp(t *testing.T) {
	since := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	st := State{Phase: Connected, Text: "Connected", Since: since}

	assert.Equal(t, "Connected", st.Label(since.Add(30*time.Second)))
	assert.Equal(t, "Connected · 5m", st.Label(since.Add(5*time.Minute+10*time.Second)))
	assert.Equal(t, "Connected · 1h 20m", st.Label(since.Add(80*time.Minute)))
	assert.Equal(t, "Connected · 2h 0m", st.Label(since.Add(2*time.Hour)))
}

func TestOnlyAConnectedStateGetsTheDuration(t *testing.T) {
	st := State{Phase: Waiting, Text: "Waiting for approval", Since: time.Now().Add(-time.Hour)}

	assert.Equal(t, "Waiting for approval", st.Label(time.Now()))
}

func TestAdoptingATunnelKeepsWhenItCameUp(t *testing.T) {
	a, _, d := rig()
	since := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	d.set(ipc.Status{State: "up", SessionID: "s1", Since: since})

	require.True(t, a.Adopt())

	assert.Equal(t, since, a.State().Since)
}

func TestSignOutDisconnectsAndThenForgetsTheSignIn(t *testing.T) {
	a, s, d := rig()
	forgot := false
	a.Forget = func() { forgot = true }
	a.Connect()
	require.Eventually(t, func() bool { return a.State().Phase == Connected }, time.Second, time.Millisecond)

	a.SignOut()

	assert.True(t, forgot)
	assert.Equal(t, Idle, a.State().Phase)
	assert.Equal(t, []string{"s1"}, s.closedIDs())
	assert.Contains(t, d.opList(), "down")
}

func TestSignOutReportsTheStateAgainAfterTheSignInIsGone(t *testing.T) {
	a, _, _ := rig()
	signedIn := true
	a.Forget = func() { signedIn = false }
	var seen []bool
	a.OnChange = func(State) { seen = append(seen, signedIn) }

	a.SignOut()

	require.NotEmpty(t, seen)
	assert.False(t, seen[len(seen)-1])
}

type game struct {
	mu       sync.Mutex
	running  bool
	err      error
	asked    int
	answer   Answer
	canceled chan struct{}
}

func (g *game) Running() (bool, error) { g.mu.Lock(); defer g.mu.Unlock(); return g.running, g.err }
func (g *game) set(r bool)             { g.mu.Lock(); g.running = r; g.mu.Unlock() }
func (g *game) askedCount() int        { g.mu.Lock(); defer g.mu.Unlock(); return g.asked }

func (g *game) Ask(ctx context.Context) Answer {
	g.mu.Lock()
	g.asked++
	a := g.answer
	g.mu.Unlock()
	if a == NoAnswer {
		<-ctx.Done()
		close(g.canceled)
	}
	return a
}

func watched(answer Answer) (*App, *sessions, *daemon, *game) {
	a, s, d := rig()
	g := &game{answer: answer, canceled: make(chan struct{})}
	a.Game = &Game{Running: g.Running, Ask: g.Ask, Absent: 20 * time.Millisecond, Answer: 60 * time.Millisecond}
	return a, s, d, g
}

func TestLeavingAfterTheGameClosedDisconnectsAndSaysWhy(t *testing.T) {
	a, s, d, _ := watched(Leave)

	a.Connect()

	require.Eventually(t, func() bool { return a.State().Phase == Idle && a.State().Text != "" }, time.Second, time.Millisecond)
	assert.Contains(t, a.State().Text, "game")
	assert.Contains(t, d.opList(), "down")
	assert.Equal(t, []string{"s1"}, s.closedIDs())
}

func TestNothingIsAskedWhileTheGameRuns(t *testing.T) {
	a, _, _, g := watched(Leave)
	g.set(true)

	a.Connect()

	time.Sleep(150 * time.Millisecond)
	assert.Equal(t, Connected, a.State().Phase)
	assert.Zero(t, g.askedCount())
}

func TestNoAnswerDisconnects(t *testing.T) {
	a, s, _, g := watched(NoAnswer)

	a.Connect()

	require.Eventually(t, func() bool { return a.State().Phase == Idle && a.State().Text != "" }, time.Second, time.Millisecond)
	assert.Equal(t, []string{"s1"}, s.closedIDs())
	select {
	case <-g.canceled:
	case <-time.After(time.Second):
		t.Fatal("the dialog was left open")
	}
}

func TestKeepingStaysConnected(t *testing.T) {
	a, _, _, g := watched(Keep)

	a.Connect()

	require.Eventually(t, func() bool { return g.askedCount() >= 1 }, time.Second, time.Millisecond)
	assert.Equal(t, Connected, a.State().Phase)
}

func TestNotKnowingWhetherTheGameRunsNeverDisconnects(t *testing.T) {
	a, _, _, g := watched(Leave)
	g.err = errors.New("ps failed")

	a.Connect()

	time.Sleep(150 * time.Millisecond)
	assert.Equal(t, Connected, a.State().Phase)
	assert.Zero(t, g.askedCount())
}
