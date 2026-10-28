package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ShotaKitazawa/MapleGate/internal/store"
	"github.com/ShotaKitazawa/MapleGate/node/agent/wg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var ctx = context.Background()

type fakeWG struct {
	peers   map[string]wg.Peer
	addErr  error
	failGet error
}

func newWG() *fakeWG { return &fakeWG{peers: map[string]wg.Peer{}} }

func (f *fakeWG) Peers() ([]wg.Peer, error) {
	if f.failGet != nil {
		return nil, f.failGet
	}
	out := make([]wg.Peer, 0, len(f.peers))
	for _, p := range f.peers {
		out = append(out, p)
	}
	return out, nil
}

func (f *fakeWG) Set(pub, addr string) error {
	if f.addErr != nil {
		return f.addErr
	}
	p := f.peers[pub]
	p.PublicKey, p.Address = pub, addr
	f.peers[pub] = p
	return nil
}

func (f *fakeWG) Remove(pub string) error { delete(f.peers, pub); return nil }

// flakyStore injects DynamoDB-side faults between the agent and the in-memory store.
type flakyStore struct {
	*store.Mem
	readDown  bool
	writeDown bool
	hang      bool
}

func (f *flakyStore) ListWork(c context.Context) ([]store.Work, error) {
	if f.hang {
		<-c.Done()
		return nil, c.Err()
	}
	if f.readDown {
		return nil, errors.New("dynamodb unreachable")
	}
	return f.Mem.ListWork(c)
}

func (f *flakyStore) ReportApplied(c context.Context, sid, boot string, at time.Time) error {
	if f.writeDown {
		return errors.New("write failed")
	}
	return f.Mem.ReportApplied(c, sid, boot, at)
}

func (f *flakyStore) PutNode(c context.Context, n store.Node, now time.Time, d time.Duration) error {
	if f.writeDown {
		return errors.New("write failed")
	}
	return f.Mem.PutNode(c, n, now, d)
}

type env struct {
	a        *Agent
	st       *flakyStore
	wg       *fakeWG
	cache    *MemCache
	now      time.Time
	uptime   time.Duration
	poweroff int
}

func newEnv() *env {
	e := &env{st: &flakyStore{Mem: store.NewMem()}, wg: newWG(), cache: &MemCache{}, now: now, uptime: time.Hour}
	e.a = e.agent("boot1")
	return e
}

func (e *env) agent(boot string) *Agent {
	return &Agent{
		Cfg: Config{
			Subnet: subnet, MaxPeers: 16, CallTimeout: 50 * time.Millisecond, BlindAfter: 3,
			BootGrace: 10 * time.Minute, IdleStop: 15 * time.Minute, BlindPeer0Stop: 30 * time.Minute,
			LocalHandshakeTTL: 10 * time.Minute, Heartbeat: 30 * time.Second, HandshakeEvery: time.Minute,
			TakeoverAfter: 5 * time.Minute,
		},
		Store: e.st, WG: e.wg, Cache: e.cache,
		Now:        func() time.Time { return e.now },
		Uptime:     func() time.Duration { return e.uptime },
		Poweroff:   func() error { e.poweroff++; return nil },
		InstanceID: "i-1", BootID: boot, PublicIP: "1.2.3.4",
	}
}

func (e *env) tick(t *testing.T) {
	require.NoError(t, e.a.Tick(ctx))
}

func (e *env) advance(d time.Duration) { e.now = e.now.Add(d); e.uptime += d }

func (e *env) approve(t *testing.T, sid string, n byte) store.Work {
	require.NoError(t, e.st.CreateSession(ctx, store.Session{
		ID: sid, User: "u-" + sid, PublicKey: key(n), Status: store.StatusPending, ExpiresAt: e.now.Add(15 * time.Minute),
	}, e.now))
	require.NoError(t, e.st.Transition(ctx, sid, []store.Status{store.StatusPending}, store.StatusApproved, store.Update{
		Address: "10.66.0." + string(rune('0'+n)), ExpiresAt: e.now.Add(4 * time.Hour), ApprovedAt: e.now,
	}))
	w, _ := e.st.GetWork(ctx, sid)
	return w
}

func (e *env) work(sid string) store.Work {
	w, _ := e.st.GetWork(ctx, sid)
	return w
}

func TestAppliesApprovedPeerAndReports(t *testing.T) {
	e := newEnv()
	w := e.approve(t, "s1", 2)

	e.tick(t)

	assert.Contains(t, e.wg.peers, w.PublicKey)
	assert.Equal(t, "boot1", e.work("s1").AppliedBootID)
	n, err := e.st.GetNode(ctx)
	require.NoError(t, err)
	assert.Equal(t, "boot1", n.BootID)
	assert.Equal(t, "1.2.3.4", n.PublicIP)
	assert.Equal(t, store.Visible(store.StatusApproved, e.work("s1"), n, e.now, 2*time.Minute), "active")
}

func TestRebootReappliesEverything(t *testing.T) {
	e := newEnv()
	w := e.approve(t, "s1", 2)
	e.tick(t)

	e.wg = newWG()
	e.a = e.agent("boot2")
	e.a.WG = e.wg
	e.tick(t)

	assert.Contains(t, e.wg.peers, w.PublicKey, "peers come back from DynamoDB, not from memory")
	assert.Equal(t, "boot2", e.work("s1").AppliedBootID)
}

func TestRestartedAgentKeepsPeersAndResumesFromCache(t *testing.T) {
	e := newEnv()
	w := e.approve(t, "s1", 2)
	e.tick(t)

	e.a = e.agent("boot1")
	e.st.readDown = true
	for i := 0; i < 3; i++ {
		e.tick(t)
	}

	assert.Equal(t, ModeBlind, e.a.Mode())
	assert.Contains(t, e.wg.peers, w.PublicKey)
	e.advance(5 * time.Hour)
	e.tick(t)
	assert.NotContains(t, e.wg.peers, w.PublicKey, "the cached expiry still removes it")
}

func TestDeleteRemovesPeerAndConfirms(t *testing.T) {
	e := newEnv()
	w := e.approve(t, "s1", 2)
	e.tick(t)

	require.NoError(t, e.st.Transition(ctx, "s1", []store.Status{store.StatusApproved}, store.StatusClosed, store.Update{CloseReason: "user"}))
	e.advance(10 * time.Second)
	e.tick(t)

	assert.NotContains(t, e.wg.peers, w.PublicKey)
	assert.False(t, e.work("s1").RemovedAt.IsZero(), "the janitor frees the IP only after this")
}

func TestPendingSessionIsNotApplied(t *testing.T) {
	e := newEnv()
	require.NoError(t, e.st.CreateSession(ctx, store.Session{ID: "s1", User: "a", PublicKey: key(2), Status: store.StatusPending, ExpiresAt: e.now.Add(time.Hour)}, e.now))
	e.tick(t)
	assert.Empty(t, e.wg.peers)
}

func TestEmptyQueueDoesNotWipePeers(t *testing.T) {
	e := newEnv()
	w := e.approve(t, "s1", 2)
	e.tick(t)

	e.st.Mem = store.NewMem() // wrong table or region: the queue looks empty
	e.advance(10 * time.Second)
	e.tick(t)

	assert.Contains(t, e.wg.peers, w.PublicKey)
}

func TestBlindModeKeepsPeersAndSelfGoverns(t *testing.T) {
	e := newEnv()
	w := e.approve(t, "s1", 2)
	e.tick(t)
	e.st.readDown = true

	for i := 0; i < 3; i++ {
		e.advance(10 * time.Second)
		e.tick(t)
	}
	assert.Equal(t, ModeBlind, e.a.Mode())
	assert.Contains(t, e.wg.peers, w.PublicKey, "playing continues through the outage")

	// the peer goes quiet: blind mode removes it on its own
	e.advance(11 * time.Minute)
	e.tick(t)
	assert.NotContains(t, e.wg.peers, w.PublicKey)
}

func TestBlindModeKeepsAChattyPeerUntilItsOwnExpiry(t *testing.T) {
	e := newEnv()
	w := e.approve(t, "s1", 2)
	e.tick(t)
	e.st.readDown = true
	for i := 0; i < 3; i++ {
		e.tick(t)
	}

	chat := func(n int) {
		for i := 0; i < n; i++ {
			e.advance(9 * time.Minute)
			if p, ok := e.wg.peers[w.PublicKey]; ok {
				p.LastHandshake = e.now
				e.wg.peers[w.PublicKey] = p
			}
			e.tick(t)
		}
	}
	chat(25)
	assert.Contains(t, e.wg.peers, w.PublicKey, "3h45m of handshakes keeps it")
	chat(5)
	assert.NotContains(t, e.wg.peers, w.PublicKey, "the 4 hour expiry still wins")
}

func TestBlindModePowersOffWithoutPeers(t *testing.T) {
	e := newEnv()
	e.st.readDown = true
	for i := 0; i < 3; i++ {
		e.tick(t)
	}
	e.advance(29 * time.Minute)
	e.tick(t)
	assert.Equal(t, 0, e.poweroff)
	e.advance(2 * time.Minute)
	e.tick(t)
	assert.Equal(t, 1, e.poweroff)
}

func TestBlindModeRecovers(t *testing.T) {
	e := newEnv()
	w := e.approve(t, "s1", 2)
	e.tick(t)
	e.st.readDown = true
	for i := 0; i < 3; i++ {
		e.tick(t)
	}
	require.NoError(t, e.st.Transition(ctx, "s1", []store.Status{store.StatusApproved}, store.StatusClosed, store.Update{}))

	e.st.readDown = false
	e.advance(10 * time.Second)
	e.tick(t)

	assert.Equal(t, ModeNormal, e.a.Mode())
	assert.NotContains(t, e.wg.peers, w.PublicKey, "the revocation made during the outage lands on recovery")
}

func TestTransientFailuresDoNotGoBlind(t *testing.T) {
	e := newEnv()
	e.st.readDown = true
	e.tick(t)
	e.tick(t)
	e.st.readDown = false
	e.tick(t)
	e.st.readDown = true
	e.tick(t)
	assert.Equal(t, ModeNormal, e.a.Mode())
}

func TestWriteFailuresDoNotStopLocalApply(t *testing.T) {
	e := newEnv()
	w := e.approve(t, "s1", 2)
	e.st.writeDown = true

	e.tick(t)

	assert.Contains(t, e.wg.peers, w.PublicKey, "a heartbeat/report outage must not take the tunnel down")
	assert.Equal(t, "", e.work("s1").AppliedBootID)

	e.st.writeDown = false
	e.advance(10 * time.Second)
	e.tick(t)
	assert.Equal(t, "boot1", e.work("s1").AppliedBootID, "level-triggered: the report catches up")
}

func TestHungReadIsBoundedByTheCallTimeout(t *testing.T) {
	e := newEnv()
	e.st.hang = true
	done := make(chan struct{})
	go func() { _ = e.a.Tick(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("tick hung")
	}
}

func TestWireGuardFailureIsReportedNotApplied(t *testing.T) {
	e := newEnv()
	e.approve(t, "s1", 2)
	e.wg.addErr = errors.New("netlink: no such device")

	e.tick(t)

	w := e.work("s1")
	assert.Contains(t, w.NodeError, "netlink")
	assert.Equal(t, "", w.AppliedBootID, "never claim an application that failed")
}

func TestSplitBrainRefusesToApply(t *testing.T) {
	e := newEnv()
	e.approve(t, "s1", 2)
	require.NoError(t, e.st.PutNode(ctx, store.Node{InstanceID: "i-other", BootID: "x", LastSeen: e.now}, e.now, time.Minute))

	err := e.a.Tick(ctx)

	assert.ErrorIs(t, err, ErrSplitBrain)
	assert.Empty(t, e.wg.peers)
}

func TestStopsOnlyAfterIdleGraceAndRecheck(t *testing.T) {
	e := newEnv()
	e.uptime = time.Minute
	e.tick(t)
	e.advance(20 * time.Minute) // past boot grace and past the idle window if it had been counting
	e.tick(t)
	assert.Equal(t, 0, e.poweroff, "idle clock starts only after the boot grace")

	e.advance(14 * time.Minute)
	e.tick(t)
	assert.Equal(t, 0, e.poweroff)
	e.advance(2 * time.Minute)
	e.tick(t)
	assert.Equal(t, 1, e.poweroff)
	n, _ := e.st.GetNode(ctx)
	assert.Equal(t, "stopping", n.State)
}

func TestPendingSessionKeepsTheNodeUp(t *testing.T) {
	e := newEnv()
	require.NoError(t, e.st.CreateSession(ctx, store.Session{ID: "s1", User: "a", PublicKey: key(2), Status: store.StatusPending, ExpiresAt: e.now.Add(15 * time.Minute)}, e.now))
	e.tick(t)
	e.advance(14 * time.Minute)
	e.tick(t)
	e.advance(14 * time.Minute) // the pending deadline has passed by now
	e.tick(t)
	assert.Equal(t, 0, e.poweroff, "an approval can take up to its deadline")
}

func TestExpiredPendingNoLongerKeepsTheNodeUp(t *testing.T) {
	e := newEnv()
	require.NoError(t, e.st.CreateSession(ctx, store.Session{ID: "s1", User: "a", PublicKey: key(2), Status: store.StatusPending, ExpiresAt: e.now.Add(15 * time.Minute)}, e.now))
	e.advance(16 * time.Minute)
	e.tick(t)
	e.advance(16 * time.Minute)
	e.tick(t)
	assert.Equal(t, 1, e.poweroff, "the node does not wait on a janitor that may be down")
}

func TestApprovedSessionKeepsTheNodeUp(t *testing.T) {
	e := newEnv()
	e.approve(t, "s1", 2)
	for i := 0; i < 10; i++ {
		e.advance(20 * time.Minute)
		e.tick(t)
	}
	assert.Equal(t, 0, e.poweroff)
}

func TestLateSessionCancelsTheStop(t *testing.T) {
	e := newEnv()
	e.tick(t) // idle clock starts
	e.advance(14 * time.Minute)
	e.tick(t)
	assert.Equal(t, 0, e.poweroff)
	e.advance(2 * time.Minute)
	// a session appears between the first read and the re-check
	e.a.Store = &racingStore{flakyStore: e.st, onSecondList: func() {
		_ = e.st.CreateSession(ctx, store.Session{ID: "s9", User: "z", PublicKey: key(9), Status: store.StatusPending, ExpiresAt: e.now.Add(15 * time.Minute)}, e.now)
	}}
	e.tick(t)
	assert.Equal(t, 0, e.poweroff)
}

type racingStore struct {
	*flakyStore
	calls        int
	onSecondList func()
}

func (r *racingStore) ListWork(c context.Context) ([]store.Work, error) {
	r.calls++
	if r.calls == 2 {
		r.onSecondList()
	}
	return r.flakyStore.ListWork(c)
}
