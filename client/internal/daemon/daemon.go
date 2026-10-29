// Package daemon owns the one tunnel of the machine and ends it when nobody is looking after it.
package daemon

import (
	"context"
	"sync"
	"time"

	"github.com/ShotaKitazawa/MapleGate/client/internal/ipc"
	"github.com/ShotaKitazawa/MapleGate/client/internal/tunnel"
)

type Manager struct {
	// Run holds a tunnel until ctx is done; it calls ready when the interface and routes are in place.
	Run func(ctx context.Context, u tunnel.Up, ready func(iface string)) error
	Now func() time.Time
	// Orphan is how long the tunnel may go without a call before it is taken down.
	Orphan time.Duration

	mu       sync.Mutex
	st       ipc.Status
	cancel   context.CancelFunc
	done     chan struct{}
	gen      int
	lastSeen time.Time
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

// stop ends the running tunnel and waits for its cleanup; the caller holds no lock.
func (m *Manager) stop() {
	m.mu.Lock()
	cancel, done := m.cancel, m.done
	m.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

func (m *Manager) Up(req ipc.Request) ipc.Status {
	m.stop()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastSeen = m.now()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	m.gen++
	gen := m.gen
	m.cancel, m.done = cancel, done
	m.st = ipc.Status{State: "connecting", SessionID: req.SessionID, Since: m.now()}
	go func() {
		defer close(done)
		err := m.Run(ctx, *req.Up, func(iface string) {
			m.mu.Lock()
			defer m.mu.Unlock()
			if m.gen == gen {
				m.st.State, m.st.Iface = "up", iface
			}
		})
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.gen != gen {
			return
		}
		m.st = ipc.Status{State: "down"}
		m.cancel = nil
		if err != nil && ctx.Err() == nil {
			m.st.Error = err.Error()
		}
	}()
	return m.st
}

func (m *Manager) Down() ipc.Status {
	m.stop()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastSeen = m.now()
	m.cancel = nil
	m.st = ipc.Status{State: "down"}
	return m.st
}

func (m *Manager) Status() ipc.Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastSeen = m.now()
	return m.st
}

// Check takes the tunnel down if it has gone unwatched for too long, and reports whether it did.
func (m *Manager) Check() bool {
	m.mu.Lock()
	held := m.st.State == "connecting" || m.st.State == "up"
	orphan := held && m.now().Sub(m.lastSeen) > m.Orphan
	m.mu.Unlock()
	if orphan {
		m.Down()
	}
	return orphan
}

// Watch calls Check every interval until ctx is done.
func (m *Manager) Watch(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.Check()
		}
	}
}
