// Package app is the logic of the tray app without any UI: it connects, watches and disconnects, and reports a state.
package app

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ShotaKitazawa/MapleGate/client/internal/api"
	"github.com/ShotaKitazawa/MapleGate/client/internal/gamewatch"
	"github.com/ShotaKitazawa/MapleGate/client/internal/ipc"
	"github.com/ShotaKitazawa/MapleGate/client/internal/tunnel"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

const (
	Idle      = "idle"
	Waiting   = "waiting" // signing in and waiting for the Discord approval
	Connected = "connected"
	Failed    = "failed"
)

type State struct {
	Phase string
	Text  string
	// Since is when the tunnel came up. How long it has been up says more than the session's expiry, which moves while it is in use.
	Since time.Time
}

// Label is the text for the menu; a connected state also says how long it has been up.
func (s State) Label(now time.Time) string {
	if s.Phase != Connected || s.Since.IsZero() || now.Sub(s.Since) < time.Minute {
		return s.Text
	}
	d := now.Sub(s.Since)
	if d < time.Hour {
		return fmt.Sprintf("%s · %dm", s.Text, int(d.Minutes()))
	}
	return fmt.Sprintf("%s · %dh %dm", s.Text, int(d.Hours()), int(d.Minutes())%60)
}

type Sessions interface {
	Request(ctx context.Context, publicKey string, wait time.Duration) (api.Session, error)
	Watch(ctx context.Context, id string) (api.Session, error)
	Close(id string) error
}

type Daemon interface {
	Do(ipc.Request) (ipc.Status, error)
}

type Answer int

const (
	NoAnswer Answer = iota
	Keep
	Leave
)

// Game is how the app tells that the tunnel is no longer used; without it the tunnel is never ended for that reason.
type Game struct {
	// Running errs on the side of "running": not knowing never ends a tunnel.
	Running func() (bool, error)
	// Ask shows the question and returns when it is answered or ctx is done.
	Ask            func(ctx context.Context) Answer
	Absent, Answer time.Duration
}

type App struct {
	Sessions Sessions
	Daemon   Daemon
	Wait     time.Duration
	// Poll is how often the daemon is asked for its status, which is also its heartbeat.
	Poll     time.Duration
	OnChange func(State)
	// Forget drops the cached sign in; SignOut calls it after the tunnel is down.
	Forget func()
	Game   *Game

	mu     sync.Mutex
	state  State
	cancel context.CancelFunc
	gen    int
	// session is the connected session, kept so that Disconnect can close it at the server.
	session string
}

func (a *App) State() State {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state
}

func (a *App) set(gen int, s State) {
	a.mu.Lock()
	if gen != a.gen {
		a.mu.Unlock()
		return
	}
	a.state = s
	f := a.OnChange
	a.mu.Unlock()
	if f != nil {
		f(s)
	}
}

// Say reports progress while waiting; the session runner and the sign in call it.
func (a *App) Say(msg string) {
	a.mu.Lock()
	gen, waiting := a.gen, a.state.Phase == Waiting
	a.mu.Unlock()
	if waiting {
		a.set(gen, State{Phase: Waiting, Text: msg})
	}
}

// begin starts a new run and cancels the previous one.
func (a *App) begin(s State) (context.Context, int) {
	a.mu.Lock()
	if a.cancel != nil {
		a.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	a.gen++
	gen := a.gen
	a.mu.Unlock()
	a.set(gen, s)
	return ctx, gen
}

// Connect runs in the background; the outcome arrives through OnChange.
func (a *App) Connect() {
	if p := a.State().Phase; p == Waiting || p == Connected {
		return
	}
	ctx, gen := a.begin(State{Phase: Waiting, Text: "Signing in"})
	go a.connect(ctx, gen)
}

func (a *App) connect(ctx context.Context, gen int) {
	key, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		a.set(gen, State{Phase: Failed, Text: err.Error()})
		return
	}
	s, err := a.Sessions.Request(ctx, key.PublicKey().String(), a.Wait)
	if err != nil {
		if s.ID != "" && !s.Terminal() {
			_ = a.Sessions.Close(s.ID)
		}
		if ctx.Err() == nil {
			a.set(gen, State{Phase: Failed, Text: err.Error()})
		}
		return
	}
	a.mu.Lock()
	if a.gen == gen {
		a.session = s.ID
	}
	a.mu.Unlock()
	c := s.Config
	a.Say("Bringing the tunnel up")
	st, err := a.Daemon.Do(ipc.Request{Op: "up", SessionID: s.ID, Up: &tunnel.Up{
		PrivateKey: key.String(), Address: c.Address, ServerKey: c.ServerKey,
		Endpoint: c.Endpoint, AllowedIPs: c.AllowedIPs, Keepalive: c.Keepalive,
	}})
	if err != nil {
		_ = a.Sessions.Close(s.ID)
		a.set(gen, State{Phase: Failed, Text: err.Error()})
		return
	}
	since := st.Since
	if since.IsZero() {
		since = time.Now()
	}
	a.set(gen, State{Phase: Connected, Text: "Connected", Since: since})
	a.hold(ctx, gen, s.ID)
}

// hold watches a connected session and the daemon until either ends or ctx is done.
func (a *App) hold(ctx context.Context, gen int, id string) {
	ended := make(chan api.Session, 1)
	go func() {
		if s, err := a.Sessions.Watch(ctx, id); err == nil {
			ended <- s
		}
	}()
	poll := a.Poll
	if poll <= 0 {
		poll = 5 * time.Second
	}
	t := time.NewTicker(poll)
	defer t.Stop()
	gw := a.newGameWatch(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case s := <-ended:
			_, _ = a.Daemon.Do(ipc.Request{Op: "down"})
			a.set(gen, State{Phase: Failed, Text: fmt.Sprintf("The session ended: %s", s.Status)})
			return
		case <-t.C:
			if gw != nil && gw.tick() {
				a.disconnect("Disconnected: the game was not running")
				return
			}
			st, err := a.Daemon.Do(ipc.Request{Op: "status"})
			if err != nil || st.State != "down" {
				continue
			}
			_ = a.Sessions.Close(id)
			text := "The tunnel stopped"
			if st.Error != "" {
				text += ": " + st.Error
			}
			a.set(gen, State{Phase: Failed, Text: text})
			return
		}
	}
}

// gameWatch asks whether to disconnect once the game has been gone for a while.
type gameWatch struct {
	g       *Game
	w       *gamewatch.Watcher
	ctx     context.Context
	cancel  context.CancelFunc
	answers chan Answer
}

func (a *App) newGameWatch(ctx context.Context) *gameWatch {
	if a.Game == nil {
		return nil
	}
	return &gameWatch{g: a.Game, w: gamewatch.New(a.Game.Absent, a.Game.Answer, time.Now()), ctx: ctx, answers: make(chan Answer, 1)}
}

// tick reports whether the tunnel should be ended now.
func (gw *gameWatch) tick() bool {
	select {
	case ans := <-gw.answers:
		switch ans {
		case Leave:
			return true
		case Keep:
			gw.w.Keep(time.Now())
		}
	default:
	}
	running, err := gw.g.Running()
	switch gw.w.Tick(time.Now(), running || err != nil) {
	case gamewatch.Ask:
		var ctx context.Context
		ctx, gw.cancel = context.WithCancel(gw.ctx)
		go func() { gw.answers <- gw.g.Ask(ctx) }()
	case gamewatch.Cancel:
		gw.stopAsking()
	case gamewatch.Disconnect:
		gw.stopAsking()
		return true
	}
	return false
}

func (gw *gameWatch) stopAsking() {
	if gw.cancel != nil {
		gw.cancel()
	}
}

// Disconnect ends the tunnel and the session.
func (a *App) Disconnect() { a.disconnect("") }

func (a *App) disconnect(why string) {
	a.mu.Lock()
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	a.gen++
	gen := a.gen
	id := a.session
	a.session = ""
	a.mu.Unlock()

	_, _ = a.Daemon.Do(ipc.Request{Op: "down"})
	if id != "" {
		_ = a.Sessions.Close(id)
	}
	a.set(gen, State{Phase: Idle, Text: why})
}

// SignOut ends any tunnel first, so the session is closed while there are still credentials to close it with.
func (a *App) SignOut() {
	a.Disconnect()
	if a.Forget != nil {
		a.Forget()
	}
	// the menu reads the sign in state when it is told of a change
	a.mu.Lock()
	st, f := a.state, a.OnChange
	a.mu.Unlock()
	if f != nil {
		f(st)
	}
}

// Adopt picks up a tunnel that is already up, as after the app was restarted, and keeps looking after it.
func (a *App) Adopt() bool {
	st, err := a.Daemon.Do(ipc.Request{Op: "status"})
	if err != nil || st.State != "up" || st.SessionID == "" {
		return false
	}
	ctx, gen := a.begin(State{Phase: Connected, Text: "Connected", Since: st.Since})
	a.mu.Lock()
	a.session = st.SessionID
	a.mu.Unlock()
	go a.hold(ctx, gen, st.SessionID)
	return true
}
