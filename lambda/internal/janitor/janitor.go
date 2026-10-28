package janitor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ShotaKitazawa/MapleGate/internal/store"
	"github.com/ShotaKitazawa/MapleGate/lambda/internal/discord"
	"github.com/ShotaKitazawa/MapleGate/lambda/internal/ec2ctl"
)

type Config struct {
	ProvisionTimeout time.Duration
	HandshakeIdle    time.Duration
	StaleAfter       time.Duration
	MsgSyncGiveUp    time.Duration
	StartCooldown    time.Duration
	BreakerLimit     int

	InstanceID        string
	UnresponsiveAfter time.Duration
	OverstayAfter     time.Duration
	AlertRepeat       time.Duration
}

// Janitor reclaims every transient state and retries every side effect; each pass is safe to repeat or skip.
type Janitor struct {
	Cfg       Config
	Store     store.Store
	EC2       ec2ctl.Controller
	Discord   discord.Client
	ChannelID func(ctx context.Context) (string, error)
	Now       func() time.Time
}

func (j *Janitor) Run(ctx context.Context) error {
	now := j.Now()
	state, err := j.EC2.State(ctx)
	if err != nil {
		slog.Warn("instance state", "err", err)
		state = ""
	}
	node, err := j.Store.GetNode(ctx)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("get node: %w", err)
	}
	works, err := j.Store.ListWork(ctx)
	if err != nil {
		return fmt.Errorf("list work: %w", err)
	}

	var live []store.Work
	for _, w := range works {
		w.Phase = j.sweep(ctx, w, node, state, now)
		if w.Phase.Live() {
			live = append(live, w)
		}
	}
	if len(live) > 0 {
		j.wake(ctx, live, node, state, now)
	}

	h, err := j.Store.GetHealth(ctx)
	if err != nil {
		return fmt.Errorf("get health: %w", err)
	}
	h.LastRun = now
	j.watch(ctx, &h, state, node, len(live), now)
	return j.Store.PutHealth(ctx, h)
}

// sweep applies the deadline rules to one item and returns its phase afterwards.
func (j *Janitor) sweep(ctx context.Context, w store.Work, node store.Node, state ec2ctl.State, now time.Time) store.Status {
	sess, err := j.Store.GetSession(ctx, w.SID)
	orphan := errors.Is(err, store.ErrNotFound)
	if err != nil && !orphan {
		slog.Error("get session", "session", w.SID, "err", err)
		return w.Phase
	}
	if orphan {
		// META was removed by TTL; WORK only needs the node's confirmation before it can go.
		sess = store.Session{ID: w.SID, User: w.User, Status: w.Phase, Address: w.Address}
		if w.Phase.Live() {
			w.Phase = store.StatusExpired
		}
	}

	phase := w.Phase
	if !orphan {
		phase = j.deadlines(ctx, w, node, now)
	}
	if !phase.Terminal() {
		return phase
	}
	j.retire(ctx, w, sess, phase, state, now)
	return phase
}

func (j *Janitor) deadlines(ctx context.Context, w store.Work, node store.Node, now time.Time) store.Status {
	move := func(from, to store.Status, u store.Update) store.Status {
		err := j.Store.Transition(ctx, w.SID, []store.Status{from}, to, u)
		if err == nil {
			return to
		}
		if !errors.Is(err, store.ErrConflict) {
			slog.Error("transition", "session", w.SID, "to", to, "err", err)
		}
		return from
	}

	switch w.Phase {
	case store.StatusPending:
		if now.After(w.ExpiresAt) {
			return move(store.StatusPending, store.StatusExpired, store.Update{CloseReason: "approval timeout"})
		}
	case store.StatusApproved:
		switch {
		case now.After(w.ExpiresAt):
			return move(store.StatusApproved, store.StatusExpired, store.Update{CloseReason: "expired"})
		case w.AppliedAt.IsZero() && now.Sub(w.ApprovedAt) > j.Cfg.ProvisionTimeout:
			return move(store.StatusApproved, store.StatusFailed,
				store.Update{Error: "the node did not apply the peer", CloseReason: "error"})
		case store.Visible(w.Phase, w, node, now, j.Cfg.StaleAfter) == "active" && now.Sub(lastSign(w)) > j.Cfg.HandshakeIdle:
			return move(store.StatusApproved, store.StatusClosed, store.Update{CloseReason: "idle"})
		}
	}
	return w.Phase
}

// lastSign is the newest evidence the peer is in use; applying counts so a node restart restarts the clock.
func lastSign(w store.Work) time.Time {
	t := w.ApprovedAt
	for _, c := range []time.Time{w.AppliedAt, w.LastHandshakeAt} {
		if c.After(t) {
			t = c
		}
	}
	return t
}

func (j *Janitor) retire(ctx context.Context, w store.Work, sess store.Session, phase store.Status, state ec2ctl.State, now time.Time) {
	msgDone := w.MsgSynced || w.DiscordMessageID == "" || j.syncMessage(ctx, w, sess, phase, now)

	if w.RemovedAt.IsZero() {
		// A stopped node holds no peers, so its confirmation can be given on its behalf.
		if !nodeDown(state) {
			return
		}
		if err := j.Store.MarkRemoved(ctx, w.SID, now); err != nil {
			slog.Error("mark removed", "session", w.SID, "err", err)
			return
		}
	}
	if !msgDone {
		return
	}
	_ = j.Store.ClearActive(ctx, sess.User, w.SID)
	// WORK goes first: if we crash before the release, the lease only lingers until its TTL.
	if err := j.Store.DeleteWork(ctx, w.SID); err != nil {
		slog.Error("delete work", "session", w.SID, "err", err)
		return
	}
	if w.Address != "" {
		if err := j.Store.ReleaseIP(ctx, w.Address, w.SID); err != nil {
			slog.Error("release ip", "session", w.SID, "err", err)
		}
	}
}

func nodeDown(s ec2ctl.State) bool {
	return s == ec2ctl.StateStopped || s == ec2ctl.StateTerminated || s == ec2ctl.StateMissing
}

// syncMessage reports whether the message no longer needs attention.
func (j *Janitor) syncMessage(ctx context.Context, w store.Work, sess store.Session, phase store.Status, now time.Time) bool {
	ch, err := j.ChannelID(ctx)
	if err == nil {
		m := discord.ResultMessage(sess.ID, sess.User, string(phase), sess.Approver, now.UTC().Format(time.RFC3339))
		err = j.Discord.UpdateMessage(ctx, ch, w.DiscordMessageID, m)
	}
	if err == nil {
		_ = j.Store.SetMsgSynced(ctx, w.SID, true)
		return true
	}
	var api *discord.APIError
	gone := errors.As(err, &api) && api.Status == 404
	if gone || now.Sub(w.ExpiresAt) > j.Cfg.MsgSyncGiveUp {
		_ = j.Store.SetMsgSynced(ctx, w.SID, true)
		return true
	}
	slog.Warn("sync message", "session", w.SID, "err", err)
	return false
}

// wake starts a stopped node for approved sessions, behind a breaker so a node that never comes up cannot loop forever.
func (j *Janitor) wake(ctx context.Context, live []store.Work, node store.Node, state ec2ctl.State, now time.Time) {
	switch state {
	case ec2ctl.StateMissing, ec2ctl.StateTerminated, ec2ctl.StateShuttingDown:
		j.failAll(ctx, live, "the VPN instance is unavailable")
		return
	case ec2ctl.StateStopped:
	default:
		return
	}
	// Someone waiting for approval is no reason to boot the node: a refusal or a request nobody answers would cost a start.
	if !anyApproved(live) {
		return
	}

	h, err := j.Store.GetHealth(ctx)
	if err != nil {
		slog.Error("get health", "err", err)
		return
	}
	if now.Before(h.CooldownUntil) {
		return
	}
	// Failed start calls leave LastStartAt untouched, so their count must survive until a heartbeat proves health.
	if !h.LastStartAt.IsZero() {
		if node.LastSeen.Before(h.LastStartAt) {
			h.StartFailures++
		} else {
			h.StartFailures = 0
		}
	}
	if h.StartFailures >= j.Cfg.BreakerLimit {
		j.failAll(ctx, live, "the VPN node did not come up")
		h.StartFailures, h.CooldownUntil = 0, now.Add(j.Cfg.StartCooldown)
		j.putHealth(ctx, h)
		return
	}
	if err := j.EC2.EnsureStarted(ctx); err != nil {
		slog.Error("start instance", "err", err)
		h.StartFailures++
	} else {
		h.LastStartAt = now
	}
	j.putHealth(ctx, h)
}

func anyApproved(live []store.Work) bool {
	for _, w := range live {
		if w.Phase == store.StatusApproved {
			return true
		}
	}
	return false
}

func (j *Janitor) putHealth(ctx context.Context, h store.Health) {
	if err := j.Store.PutHealth(ctx, h); err != nil {
		slog.Error("put health", "err", err)
	}
}

func (j *Janitor) failAll(ctx context.Context, live []store.Work, reason string) {
	for _, w := range live {
		err := j.Store.Transition(ctx, w.SID, []store.Status{w.Phase}, store.StatusFailed,
			store.Update{Error: reason, CloseReason: "error"})
		if err != nil && !errors.Is(err, store.ErrConflict) {
			slog.Error("fail session", "session", w.SID, "err", err)
		}
	}
}
