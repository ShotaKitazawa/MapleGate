package daemon

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ShotaKitazawa/MapleGate/client/internal/ipc"
	"github.com/ShotaKitazawa/MapleGate/client/internal/tunnel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type runs struct {
	mu      sync.Mutex
	started int
	stopped int
	fail    error
}

func (r *runs) run(ctx context.Context, _ tunnel.Up, ready func(string)) error {
	r.mu.Lock()
	r.started++
	n, fail := r.started, r.fail
	r.mu.Unlock()
	if fail != nil {
		return fail
	}
	ready("utun" + string(rune('0'+n)))
	<-ctx.Done()
	r.mu.Lock()
	r.stopped++
	r.mu.Unlock()
	return nil
}

func (r *runs) counts() (int, int) { r.mu.Lock(); defer r.mu.Unlock(); return r.started, r.stopped }

func rig() (*Manager, *runs, *clock) {
	r, c := &runs{}, &clock{t: time.Unix(1000, 0)}
	return &Manager{Run: r.run, Now: c.now, Orphan: 15 * time.Minute}, r, c
}

func up(id string) ipc.Request { return ipc.Request{Op: "up", Up: &tunnel.Up{}, SessionID: id} }

func eventually(t *testing.T, f func() bool) {
	require.Eventually(t, f, time.Second, time.Millisecond)
}

func TestUpBecomesUpWhenTheTunnelIsReady(t *testing.T) {
	m, _, _ := rig()

	m.Up(up("s1"))

	eventually(t, func() bool { return m.Status().State == "up" })
	assert.Equal(t, "s1", m.Status().SessionID)
	assert.Equal(t, "utun1", m.Status().Iface)
}

func TestASecondUpReplacesTheFirstAndWaitsForItsCleanup(t *testing.T) {
	m, r, _ := rig()
	m.Up(up("s1"))
	eventually(t, func() bool { return m.Status().State == "up" })

	m.Up(up("s2"))

	eventually(t, func() bool { return m.Status().State == "up" && m.Status().SessionID == "s2" })
	started, stopped := r.counts()
	assert.Equal(t, 2, started)
	assert.Equal(t, 1, stopped)
}

func TestDownEndsTheTunnelAndWaitsForItsCleanup(t *testing.T) {
	m, r, _ := rig()
	m.Up(up("s1"))
	eventually(t, func() bool { return m.Status().State == "up" })

	st := m.Down()

	assert.Equal(t, "down", st.State)
	_, stopped := r.counts()
	assert.Equal(t, 1, stopped)
}

func TestATunnelThatFailsLeavesItsErrorUntilTheNextUp(t *testing.T) {
	m, r, _ := rig()
	r.fail = errors.New("no route")

	m.Up(up("s1"))

	eventually(t, func() bool { return m.Status().State == "down" })
	assert.Equal(t, "no route", m.Status().Error)
	r.fail = nil
	m.Up(up("s2"))
	eventually(t, func() bool { return m.Status().State == "up" })
	assert.Empty(t, m.Status().Error)
}

func TestATunnelNobodyLooksAfterIsTakenDown(t *testing.T) {
	m, r, c := rig()
	m.Up(up("s1"))
	eventually(t, func() bool { return m.Status().State == "up" })

	c.add(16 * time.Minute)

	assert.True(t, m.Check())
	_, stopped := r.counts()
	assert.Equal(t, 1, stopped)
	assert.Equal(t, "down", m.Status().State)
}

func TestACallResetsTheOrphanClock(t *testing.T) {
	m, _, c := rig()
	m.Up(up("s1"))
	eventually(t, func() bool { return m.Status().State == "up" })

	c.add(10 * time.Minute)
	m.Status()
	c.add(10 * time.Minute)

	assert.False(t, m.Check())
	assert.Equal(t, "up", m.Status().State)
}

func TestNothingRunningIsNeverAnOrphan(t *testing.T) {
	m, _, c := rig()
	c.add(time.Hour)

	assert.False(t, m.Check())
}
