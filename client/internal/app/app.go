// Package app is the logic of the tray app without any UI: it connects, watches and disconnects, and reports a state.
package app

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ShotaKitazawa/MapleGate/client/internal/api"
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
}

type Sessions interface {
	Request(ctx context.Context, publicKey string, wait time.Duration) (api.Session, error)
	Watch(ctx context.Context, id string) (api.Session, error)
	Close(id string) error
}

type Daemon interface {
	Do(ipc.Request) (ipc.Status, error)
}

type App struct {
	Sessions Sessions
	Daemon   Daemon
	Wait     time.Duration
	// Poll is how often the daemon is asked for its status, which is also its heartbeat.
	Poll     time.Duration
	OnChange func(State)

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
	_, err = a.Daemon.Do(ipc.Request{Op: "up", SessionID: s.ID, Up: &tunnel.Up{
		PrivateKey: key.String(), Address: c.Address, ServerKey: c.ServerKey,
		Endpoint: c.Endpoint, AllowedIPs: c.AllowedIPs, Keepalive: c.Keepalive,
	}})
	if err != nil {
		_ = a.Sessions.Close(s.ID)
		a.set(gen, State{Phase: Failed, Text: err.Error()})
		return
	}
	a.set(gen, State{Phase: Connected, Text: "Connected"})
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
	for {
		select {
		case <-ctx.Done():
			return
		case s := <-ended:
			_, _ = a.Daemon.Do(ipc.Request{Op: "down"})
			a.set(gen, State{Phase: Failed, Text: fmt.Sprintf("The session ended: %s", s.Status)})
			return
		case <-t.C:
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

// Disconnect ends the tunnel and the session.
func (a *App) Disconnect() {
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
	a.set(gen, State{Phase: Idle})
}

// Adopt picks up a tunnel that is already up, as after the app was restarted, and keeps looking after it.
func (a *App) Adopt() bool {
	st, err := a.Daemon.Do(ipc.Request{Op: "status"})
	if err != nil || st.State != "up" || st.SessionID == "" {
		return false
	}
	ctx, gen := a.begin(State{Phase: Connected, Text: "Connected"})
	a.mu.Lock()
	a.session = st.SessionID
	a.mu.Unlock()
	go a.hold(ctx, gen, st.SessionID)
	return true
}
