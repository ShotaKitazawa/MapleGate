package agent

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"time"

	"github.com/ShotaKitazawa/MapleGate/internal/store"
	"github.com/ShotaKitazawa/MapleGate/node/agent/wg"
)

type Mode string

const (
	ModeNormal Mode = "normal"
	ModeBlind  Mode = "blind"
)

var ErrSplitBrain = errors.New("another node is active")

type Config struct {
	Subnet            netip.Prefix
	MaxPeers          int
	CallTimeout       time.Duration
	BlindAfter        int // consecutive failed reads before going blind
	BootGrace         time.Duration
	IdleStop          time.Duration
	BlindPeer0Stop    time.Duration
	LocalHandshakeTTL time.Duration
	Heartbeat         time.Duration
	HandshakeEvery    time.Duration
	TakeoverAfter     time.Duration
}

// Cache survives agent restarts so expiry keeps being enforced while DynamoDB is unreachable.
type Cache interface {
	Load() ([]Known, error)
	Save([]Known) error
}

type Agent struct {
	Cfg        Config
	Store      store.NodeStore
	WG         wg.Interface
	Cache      Cache
	Now        func() time.Time
	Uptime     func() time.Duration
	Poweroff   func() error
	InstanceID string
	BootID     string
	PublicIP   string

	known      []Known
	loaded     bool
	registered bool
	mode       Mode
	failures   int
	blindSince time.Time
	idleSince  time.Time
	lastBeat   time.Time
	lastShake  map[string]time.Time
	lastSaved  []Known
	failedSet  map[string]bool
}

func (a *Agent) Mode() Mode { return a.mode }

// Healthy means a full pass reached DynamoDB; the rollout logic promotes a new binary on it.
func (a *Agent) Healthy() bool { return a.registered && a.mode == ModeNormal && a.failures == 0 }

// Tick runs one convergence pass. It never blocks past CallTimeout per call.
func (a *Agent) Tick(ctx context.Context) error {
	if a.mode == "" {
		a.mode = ModeNormal
	}
	if !a.loaded {
		k, err := a.Cache.Load()
		if err != nil {
			slog.Warn("load cache", "err", err)
		}
		a.known, a.loaded = k, true
	}
	now := a.Now()

	if !a.registered {
		if err := a.beat(ctx, now, true); err != nil {
			if errors.Is(err, ErrSplitBrain) {
				return err
			}
			// not being able to register is a write failure; local expiry below still applies
			slog.Warn("register node", "err", err)
		}
	}

	peers, err := a.WG.Peers()
	if err != nil {
		return err
	}

	works, err := a.list(ctx)
	if err != nil {
		a.failures++
		slog.Warn("list work", "failures", a.failures, "err", err)
		if a.failures >= a.Cfg.BlindAfter && a.mode != ModeBlind {
			a.mode, a.blindSince = ModeBlind, now
			slog.Error("entering blind mode")
		}
		if a.mode == ModeBlind {
			a.blind(peers, now)
		}
		return nil
	}
	if a.mode == ModeBlind {
		slog.Info("leaving blind mode")
	}
	a.mode, a.failures, a.blindSince = ModeNormal, 0, time.Time{}

	ops := Plan(PlanInput{
		Work: works, Peers: peers, Known: a.known, BootID: a.BootID, Subnet: a.Cfg.Subnet,
		Now: now, MaxPeers: a.Cfg.MaxPeers, HandshakeEvery: a.Cfg.HandshakeEvery,
	})
	a.apply(ctx, ops, works, now)

	if err := a.beat(ctx, now, false); err != nil && errors.Is(err, ErrSplitBrain) {
		return err
	}
	a.maybeStop(ctx, works, now)
	return nil
}

func (a *Agent) list(ctx context.Context) ([]store.Work, error) {
	ctx, cancel := context.WithTimeout(ctx, a.Cfg.CallTimeout)
	defer cancel()
	return a.Store.ListWork(ctx)
}

func (a *Agent) call(ctx context.Context, f func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, a.Cfg.CallTimeout)
	defer cancel()
	return f(ctx)
}

func (a *Agent) beat(ctx context.Context, now time.Time, force bool) error {
	if !force && now.Sub(a.lastBeat) < a.Cfg.Heartbeat {
		return nil
	}
	err := a.call(ctx, func(ctx context.Context) error {
		return a.Store.PutNode(ctx, store.Node{
			InstanceID: a.InstanceID, BootID: a.BootID, PublicIP: a.PublicIP,
			LastSeen: now, State: "running", Mode: string(a.mode),
		}, now, a.Cfg.TakeoverAfter)
	})
	switch {
	case errors.Is(err, store.ErrConflict):
		return ErrSplitBrain
	case err != nil:
		return err
	}
	a.registered, a.lastBeat = true, now
	return nil
}

func (a *Agent) apply(ctx context.Context, ops []Op, works []store.Work, now time.Time) {
	byID := map[string]store.Work{}
	for _, w := range works {
		byID[w.SID] = w
	}
	for _, op := range ops {
		switch op.Kind {
		case OpAdd:
			if err := a.WG.Set(op.PublicKey, op.Address); err != nil {
				slog.Error("add peer", "session", op.SID, "err", err)
				a.report(ctx, func(ctx context.Context) error { return a.Store.ReportError(ctx, op.SID, "wireguard: "+err.Error()) })
				a.failed(op.SID)
				continue
			}
			w := byID[op.SID]
			a.remember(Known{SID: op.SID, PublicKey: op.PublicKey, Address: op.Address, ExpiresAt: w.ExpiresAt, AppliedAt: now})
		case OpRemove:
			if err := a.WG.Remove(op.PublicKey); err != nil {
				slog.Error("remove peer", "session", op.SID, "err", err)
				a.failed(op.SID)
				continue
			}
			a.forget(op.PublicKey)
		case OpReportApplied:
			if a.isFailed(op.SID) {
				continue
			}
			a.report(ctx, func(ctx context.Context) error { return a.Store.ReportApplied(ctx, op.SID, a.BootID, op.At) })
		case OpReportRemoved:
			if a.isFailed(op.SID) {
				continue
			}
			a.report(ctx, func(ctx context.Context) error { return a.Store.ReportRemoved(ctx, op.SID, op.At) })
		case OpReportShake:
			a.report(ctx, func(ctx context.Context) error { return a.Store.ReportHandshake(ctx, op.SID, op.At) })
		case OpReportError:
			a.report(ctx, func(ctx context.Context) error { return a.Store.ReportError(ctx, op.SID, op.Message) })
		}
	}
	a.refreshExpiry(works)
	a.persist()
	a.failedSet = nil
}

func (a *Agent) refreshExpiry(works []store.Work) {
	by := map[string]store.Work{}
	for _, w := range works {
		by[w.PublicKey] = w
	}
	for i, k := range a.known {
		if w, ok := by[k.PublicKey]; ok && w.Phase == store.StatusApproved && !w.ExpiresAt.IsZero() {
			a.known[i].ExpiresAt = w.ExpiresAt
		}
	}
}

// report drops store errors: observations are level-triggered, so the next tick repeats them.
func (a *Agent) report(ctx context.Context, f func(context.Context) error) {
	if err := a.call(ctx, f); err != nil && !errors.Is(err, store.ErrNotFound) {
		slog.Warn("report", "err", err)
	}
}

func (a *Agent) remember(k Known) {
	a.forget(k.PublicKey)
	a.known = append(a.known, k)
}

func (a *Agent) forget(pub string) {
	out := a.known[:0]
	for _, k := range a.known {
		if k.PublicKey != pub {
			out = append(out, k)
		}
	}
	a.known = out
}

func (a *Agent) persist() {
	if sameKnown(a.known, a.lastSaved) {
		return
	}
	if err := a.Cache.Save(a.known); err != nil {
		slog.Warn("save cache", "err", err)
		return
	}
	a.lastSaved = append([]Known(nil), a.known...)
}

func sameKnown(x, y []Known) bool {
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// blind enforces only what the node can know alone: the expiry it cached, and peers that went quiet.
func (a *Agent) blind(peers []wg.Peer, now time.Time) {
	byKey := map[string]wg.Peer{}
	for _, p := range peers {
		byKey[p.PublicKey] = p
	}
	live := len(peers)
	for _, k := range append([]Known(nil), a.known...) {
		p, present := byKey[k.PublicKey]
		if !present {
			a.forget(k.PublicKey)
			continue
		}
		ref := k.AppliedAt
		if p.LastHandshake.After(ref) {
			ref = p.LastHandshake
		}
		if !now.Before(k.ExpiresAt) || now.Sub(ref) >= a.Cfg.LocalHandshakeTTL {
			if err := a.WG.Remove(k.PublicKey); err != nil {
				slog.Error("blind remove", "err", err)
				continue
			}
			a.forget(k.PublicKey)
			live--
		}
	}
	a.persist()
	if live == 0 && now.Sub(a.blindSince) >= a.Cfg.BlindPeer0Stop {
		slog.Error("blind with no peers, powering off")
		_ = a.Poweroff()
	}
}

func (a *Agent) maybeStop(ctx context.Context, works []store.Work, now time.Time) {
	if liveItems(works, now) > 0 || a.Uptime() < a.Cfg.BootGrace {
		a.idleSince = time.Time{}
		return
	}
	if a.idleSince.IsZero() {
		a.idleSince = now
		return
	}
	if now.Sub(a.idleSince) < a.Cfg.IdleStop {
		return
	}
	// Re-reading right before power off narrows the window in which a new session could be missed.
	again, err := a.list(ctx)
	if err != nil || liveItems(again, a.Now()) > 0 {
		a.idleSince = time.Time{}
		return
	}
	_ = a.call(ctx, func(ctx context.Context) error {
		return a.Store.PutNode(ctx, store.Node{
			InstanceID: a.InstanceID, BootID: a.BootID, PublicIP: a.PublicIP,
			LastSeen: now, State: "stopping", Mode: string(a.mode),
		}, now, a.Cfg.TakeoverAfter)
	})
	slog.Info("idle, powering off")
	_ = a.Poweroff()
}

func liveItems(works []store.Work, now time.Time) int {
	n := 0
	for _, w := range works {
		if w.Phase.NeedsNode() && now.Before(w.ExpiresAt) {
			n++
		}
	}
	return n
}

// failed marks a session whose kernel change failed this tick, so its report is withheld.
func (a *Agent) failed(sid string) {
	if a.failedSet == nil {
		a.failedSet = map[string]bool{}
	}
	a.failedSet[sid] = true
}

func (a *Agent) isFailed(sid string) bool { return a.failedSet[sid] }
