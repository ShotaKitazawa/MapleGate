// Package scenario drives the Lambda handlers, the janitor and the node agent together against
// random operation sequences and checks the lifecycle invariants (docs/lifecycle.md).
package scenario

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/netip"
	"testing"
	"time"

	"github.com/ShotaKitazawa/MapleGate/internal/store"
	"github.com/ShotaKitazawa/MapleGate/lambda/internal/config"
	"github.com/ShotaKitazawa/MapleGate/lambda/internal/discord"
	"github.com/ShotaKitazawa/MapleGate/lambda/internal/ec2ctl"
	"github.com/ShotaKitazawa/MapleGate/lambda/internal/interactions"
	"github.com/ShotaKitazawa/MapleGate/lambda/internal/janitor"
	"github.com/ShotaKitazawa/MapleGate/lambda/internal/sessionapi"
	"github.com/ShotaKitazawa/MapleGate/node/agent/agent"
	"github.com/ShotaKitazawa/MapleGate/node/agent/wg"
	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/require"
)

var ctx = context.Background()

// world is everything outside DynamoDB: the instance, its kernel peers and Discord.
type world struct {
	t   *testing.T
	rng *rand.Rand
	now time.Time

	st    *store.Mem
	nodeS *nodeStore

	state     ec2ctl.State
	stoppedAt time.Time
	bootAt    time.Time
	boots     int
	peers     map[string]wg.Peer
	cache     *agent.MemCache
	ag        *agent.Agent

	// chatty clients keep their tunnels busy, so sessions live long enough to meet the idle logic
	chatty      bool
	startFail   bool
	discordFail bool
	janitorOff  bool
	nodeCrashes int
	api         *sessionapi.Handler
	inter       *interactions.Handler
	jan         *janitor.Janitor
	created     []string
	trace       []string
}

// nodeStore is the agent's view of DynamoDB, which can be cut off independently of the Lambdas.
type nodeStore struct {
	*store.Mem
	down bool
}

func (n *nodeStore) ListWork(c context.Context) ([]store.Work, error) {
	if n.down {
		return nil, errors.New("dynamodb unreachable from the node")
	}
	return n.Mem.ListWork(c)
}

func (n *nodeStore) PutNode(c context.Context, nd store.Node, now time.Time, d time.Duration) error {
	if n.down {
		return errors.New("dynamodb unreachable from the node")
	}
	return n.Mem.PutNode(c, nd, now, d)
}

func (n *nodeStore) ReportApplied(c context.Context, sid, boot string, at time.Time) error {
	if n.down {
		return errors.New("dynamodb unreachable from the node")
	}
	return n.Mem.ReportApplied(c, sid, boot, at)
}

func (n *nodeStore) ReportRemoved(c context.Context, sid string, at time.Time) error {
	if n.down {
		return errors.New("dynamodb unreachable from the node")
	}
	return n.Mem.ReportRemoved(c, sid, at)
}

func (n *nodeStore) ReportHandshake(c context.Context, sid string, at time.Time) error {
	if n.down {
		return errors.New("dynamodb unreachable from the node")
	}
	return n.Mem.ReportHandshake(c, sid, at)
}

func (n *nodeStore) ReportError(c context.Context, sid, msg string) error {
	if n.down {
		return errors.New("dynamodb unreachable from the node")
	}
	return n.Mem.ReportError(c, sid, msg)
}

type kernel struct{ w *world }

func (k kernel) Peers() ([]wg.Peer, error) {
	out := make([]wg.Peer, 0, len(k.w.peers))
	for _, p := range k.w.peers {
		out = append(out, p)
	}
	return out, nil
}

func (k kernel) Set(pub, addr string) error {
	p := k.w.peers[pub]
	p.PublicKey, p.Address = pub, addr
	k.w.peers[pub] = p
	return nil
}

func (k kernel) Remove(pub string) error { delete(k.w.peers, pub); return nil }

type fakeEC2 struct{ w *world }

func (f fakeEC2) State(context.Context) (ec2ctl.State, error) { return f.w.state, nil }

func (f fakeEC2) EnsureStarted(context.Context) error {
	if f.w.startFail {
		return errors.New("InsufficientInstanceCapacity")
	}
	if f.w.state == ec2ctl.StateStopped {
		f.w.boot()
	}
	return nil
}

type fakeDiscord struct{ w *world }

func (f fakeDiscord) PostMessage(context.Context, string, discord.Message) (string, error) {
	if f.w.discordFail {
		return "", errors.New("discord down")
	}
	return "msg", nil
}

func (f fakeDiscord) UpdateMessage(context.Context, string, string, discord.Message) error {
	if f.w.discordFail {
		return errors.New("discord down")
	}
	return nil
}

type noInvoke struct{}

func (noInvoke) InvokeAsync(context.Context, []byte) error { return nil }

var agentCfg = agent.Config{
	Subnet: netip.MustParsePrefix("10.66.0.0/24"), MaxPeers: 16, CallTimeout: time.Second, BlindAfter: 3,
	BootGrace: 10 * time.Minute, IdleStop: 15 * time.Minute, BlindPeer0Stop: 30 * time.Minute,
	LocalHandshakeTTL: 10 * time.Minute, Heartbeat: 30 * time.Second, HandshakeEvery: time.Minute,
	TakeoverAfter: 5 * time.Minute,
}

func newWorld(t *testing.T, seed int64) *world {
	w := &world{t: t, rng: rand.New(rand.NewSource(seed)), now: time.Unix(1_700_000_000, 0), st: store.NewMem(),
		state: ec2ctl.StateStopped, cache: &agent.MemCache{}, peers: map[string]wg.Peer{}}
	w.nodeS = &nodeStore{Mem: w.st}
	cfg := config.Config{
		WGPort: 51820, WGSubnet: "10.66.0", SessionTTL: 4 * time.Hour, PendingTTL: 15 * time.Minute,
		StartRateTime: 10 * time.Minute, StaleAfter: 2 * time.Minute, AllowedIPs: []string{"0.0.0.0/0"}, Keepalive: 25,
	}
	id := 0
	w.api = &sessionapi.Handler{
		Cfg: cfg, Store: w.st, EC2: fakeEC2{w}, Discord: fakeDiscord{w}, Auth: sessionapi.FixedHeaderAuth{Token: "t"},
		ChannelID:       func(context.Context) (string, error) { return "ch", nil },
		ServerPublicKey: func(context.Context) (string, error) { return "srv", nil },
		Now:             func() time.Time { return w.now },
		NewID:           func() string { id++; return fmt.Sprintf("s%04d", id) },
	}
	w.inter = &interactions.Handler{
		Cfg: cfg, Store: w.st, EC2: fakeEC2{w}, Discord: fakeDiscord{w}, Invoker: noInvoke{},
		ChannelID: func(context.Context) (string, error) { return "ch", nil },
		Now:       func() time.Time { return w.now },
	}
	w.jan = &janitor.Janitor{
		Cfg: janitor.Config{
			ProvisionTimeout: 5 * time.Minute, HandshakeIdle: 10 * time.Minute, StaleAfter: 2 * time.Minute,
			MsgSyncGiveUp: time.Hour, StartCooldown: 15 * time.Minute, BreakerLimit: 2,
		},
		Store: w.st, EC2: fakeEC2{w}, Discord: fakeDiscord{w},
		ChannelID: func(context.Context) (string, error) { return "ch", nil },
		Now:       func() time.Time { return w.now },
	}
	return w
}

func (w *world) log(format string, a ...any) {
	w.trace = append(w.trace, fmt.Sprintf("%s "+format, append([]any{w.now.Format("15:04:05")}, a...)...))
}

func (w *world) boot() {
	w.boots++
	w.state, w.bootAt = ec2ctl.StateRunning, w.now
	w.peers = map[string]wg.Peer{}
	w.ag = &agent.Agent{
		Cfg: agentCfg, Store: w.nodeS, WG: kernel{w}, Cache: w.cache,
		Now:    func() time.Time { return w.now },
		Uptime: func() time.Duration { return w.now.Sub(w.bootAt) },
		Poweroff: func() error {
			w.state, w.stoppedAt = ec2ctl.StateStopping, w.now.Add(30*time.Second)
			w.log("poweroff")
			return nil
		},
		InstanceID: "i-1", BootID: fmt.Sprintf("boot%d", w.boots), PublicIP: "1.2.3.4",
	}
	w.log("boot %d", w.boots)
}

// poweroff must never take the node down under a live session it could have seen; only a blind node may.
func (w *world) poweroff() error {
	if !w.nodeS.down {
		for _, s := range w.st.Sessions() {
			if s.Status.Live() && w.now.Before(s.ExpiresAt) {
				w.t.Fatalf("node powered off under live session %s (%s)\n%s", s.ID, s.Status, w.dump())
			}
		}
	}
	w.state, w.stoppedAt = ec2ctl.StateStopping, w.now.Add(30*time.Second)
	w.log("poweroff")
	return nil
}

func (w *world) crash() {
	if w.state != ec2ctl.StateRunning {
		return
	}
	w.nodeCrashes++
	w.boot() // an OS reboot: peers are gone, the agent starts over from its cache
	w.log("node crashed and rebooted")
}

// step advances the clock by 10s, running the node agent every step and the janitor every minute.
func (w *world) step() {
	w.now = w.now.Add(10 * time.Second)
	if w.state == ec2ctl.StateStopping && !w.now.Before(w.stoppedAt) {
		w.state, w.peers = ec2ctl.StateStopped, map[string]wg.Peer{}
		w.log("stopped")
	}
	if w.chatty && w.rng.Intn(6) == 0 {
		w.handshakes()
	}
	if w.state == ec2ctl.StateRunning && w.ag != nil {
		_ = w.ag.Tick(ctx)
	}
	if !w.janitorOff && w.now.Unix()%60 < 10 {
		require.NoError(w.t, w.jan.Run(ctx), w.dump())
	}
	w.checkAlways()
}

func (w *world) advance(d time.Duration) {
	for end := w.now.Add(d); w.now.Before(end); {
		w.step()
	}
}

func (w *world) req(method, path, user, body string) events.LambdaFunctionURLResponse {
	r := events.LambdaFunctionURLRequest{Headers: map[string]string{"x-gamevpn-token": "t", "x-gamevpn-user": user}, Body: body}
	r.RequestContext.HTTP.Method, r.RequestContext.HTTP.Path = method, path
	return w.api.Handle(ctx, r)
}

func (w *world) create(user string) {
	key := base64.StdEncoding.EncodeToString(append(make([]byte, 31), byte(w.rng.Intn(250)+1)))
	r := w.req("POST", "/session", user, `{"public_key":"`+key+`"}`)
	w.log("create %s -> %d", user, r.StatusCode)
	if r.StatusCode == 202 {
		var m map[string]string
		require.NoError(w.t, json.Unmarshal([]byte(r.Body), &m))
		w.created = append(w.created, m["session_id"])
	}
}

func (w *world) pick() (string, bool) {
	if len(w.created) == 0 {
		return "", false
	}
	return w.created[w.rng.Intn(len(w.created))], true
}

func (w *world) get(sid string) {
	s, err := w.st.GetSession(ctx, sid)
	if err != nil {
		return
	}
	r := w.req("GET", "/session/"+sid, s.User, "")
	if r.StatusCode != 200 {
		return
	}
	var m map[string]any
	require.NoError(w.t, json.Unmarshal([]byte(r.Body), &m))
	if m["status"] != "active" {
		return
	}
	// "active" must be backed by a peer, except for the bounded window right after the node lost state
	n, _ := w.st.GetNode(ctx)
	if _, ok := w.peers[s.PublicKey]; !ok && w.now.Sub(n.LastSeen) > 2*time.Minute {
		w.t.Fatalf("session %s reported active without a peer and with a stale node\n%s", sid, w.dump())
	}
}

func (w *world) handshakes() {
	for k, p := range w.peers {
		if w.rng.Intn(3) > 0 {
			p.LastHandshake = w.now
			w.peers[k] = p
		}
	}
}

func (w *world) dump() string {
	out := "trace:\n"
	from := 0
	if len(w.trace) > 60 {
		from = len(w.trace) - 60
	}
	for _, l := range w.trace[from:] {
		out += "  " + l + "\n"
	}
	return out
}

// checkAlways holds at every step, faults or not.
func (w *world) checkAlways() {
	perUser := map[string]int{}
	addrs := map[string]string{}
	for _, s := range w.st.Sessions() {
		// past its deadline a session is dead everywhere even if no janitor has recorded it yet
		if !s.Status.Live() || !w.now.Before(s.ExpiresAt) {
			continue
		}
		perUser[s.User]++
		if perUser[s.User] > 1 {
			w.t.Fatalf("user %s has two live sessions\n%s", s.User, w.dump())
		}
		if s.Address != "" {
			if other, dup := addrs[s.Address]; dup {
				w.t.Fatalf("address %s shared by %s and %s\n%s", s.Address, other, s.ID, w.dump())
			}
			addrs[s.Address] = s.ID
		}
	}
}

func (w *world) randomOp() {
	switch n := w.rng.Intn(100); {
	case n < 18:
		w.create([]string{"alice", "bob", "carol"}[w.rng.Intn(3)])
	case n < 30:
		if sid, ok := w.pick(); ok {
			w.log("approve %s", sid)
			_ = w.inter.HandleAsync(ctx, interactions.AsyncEvent{Action: "approve", SessionID: sid, Approver: interactions.Approver{Name: "mod"}})
		}
	case n < 34:
		if sid, ok := w.pick(); ok {
			w.log("deny %s", sid)
			_ = w.inter.HandleAsync(ctx, interactions.AsyncEvent{Action: "deny", SessionID: sid, Approver: interactions.Approver{Name: "mod"}})
		}
	case n < 42:
		if sid, ok := w.pick(); ok {
			if s, err := w.st.GetSession(ctx, sid); err == nil {
				w.log("delete %s", sid)
				w.req("DELETE", "/session/"+sid, s.User, "")
			}
		}
	case n < 52:
		if sid, ok := w.pick(); ok {
			w.get(sid)
		}
	case n < 78:
		w.advance([]time.Duration{10 * time.Second, 30 * time.Second, time.Minute, 5 * time.Minute, 20 * time.Minute, time.Hour}[w.rng.Intn(6)])
	case n < 83:
		w.nodeS.down = !w.nodeS.down
		w.log("node dynamodb down=%v", w.nodeS.down)
	case n < 86:
		w.startFail = !w.startFail
		w.log("start fail=%v", w.startFail)
	case n < 89:
		w.discordFail = !w.discordFail
		w.log("discord fail=%v", w.discordFail)
	case n < 91:
		w.janitorOff = !w.janitorOff
		w.log("janitor off=%v", w.janitorOff)
	case n < 94:
		w.crash()
	default:
		w.handshakes()
	}
}

// quiesce removes every fault and lets the system settle, then checks the convergence invariants.
func (w *world) quiesce() {
	w.nodeS.down, w.startFail, w.discordFail, w.janitorOff = false, false, false, false
	w.log("--- quiesce")

	// the breaker may legitimately hold a start back for its cooldown
	w.advance(20 * time.Minute)
	w.handshakes()
	w.advance(3 * time.Minute)

	want := map[string]string{}
	for _, s := range w.st.Sessions() {
		if s.Status == store.StatusApproved && w.now.Before(s.ExpiresAt) {
			want[s.PublicKey] = s.Address
		}
	}
	for pub, addr := range want {
		p, ok := w.peers[pub]
		if !ok || p.Address != addr {
			w.t.Fatalf("approved session has no peer after settling (%v)\n%s", addr, w.dump())
		}
	}
	for pub := range w.peers {
		if _, ok := want[pub]; !ok {
			w.t.Fatalf("a peer outlived its session\n%s", w.dump())
		}
	}
	if len(want) > 0 && w.state != ec2ctl.StateRunning {
		w.t.Fatalf("live sessions but the node is %s\n%s", w.state, w.dump())
	}

	// everything must eventually be reclaimed once all deadlines have passed
	w.advance(6 * time.Hour)
	for _, s := range w.st.Sessions() {
		if !s.Status.Terminal() {
			w.t.Fatalf("session %s stuck in %s\n%s", s.ID, s.Status, w.dump())
		}
	}
	if list, _ := w.st.ListWork(ctx); len(list) > 0 {
		w.t.Fatalf("work items left behind: %+v\n%s", list, w.dump())
	}
	if l := w.st.Leases(); len(l) > 0 {
		w.t.Fatalf("leases leaked: %v\n%s", l, w.dump())
	}
	if a := w.st.ActiveUsers(); len(a) > 0 {
		w.t.Fatalf("active slots leaked: %v\n%s", a, w.dump())
	}
	if w.state != ec2ctl.StateStopped || len(w.peers) > 0 {
		w.t.Fatalf("node should be off and empty, is %s with %d peers\n%s", w.state, len(w.peers), w.dump())
	}
}

func TestRandomLifecycles(t *testing.T) {
	seeds := 300
	if testing.Short() {
		seeds = 30
	}
	for seed := int64(1); seed <= int64(seeds); seed++ {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			w := newWorld(t, seed)
			w.chatty = seed%2 == 0
			for i := 0; i < 80; i++ {
				w.randomOp()
			}
			w.quiesce()
		})
	}
}
