// Package storetest holds the contract every store implementation must satisfy.
package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/ShotaKitazawa/MapleGate/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type Full interface {
	store.Store
	store.NodeStore
}

var (
	ctx = context.Background()
	t0  = time.Unix(1_000_000, 0)
)

func pending(id, user string) store.Session {
	return store.Session{
		ID: id, User: user, PublicKey: "PUB-" + id, Status: store.StatusPending,
		CreatedAt: t0, ExpiresAt: t0.Add(15 * time.Minute),
	}
}

// Run executes the contract against fresh stores made by mk.
func Run(t *testing.T, mk func(t *testing.T) Full) {
	tests := map[string]func(*testing.T, Full){
		"create writes META and WORK":          createWritesBoth,
		"create limits":                        createLimits,
		"transition conflicts":                 transitionConflicts,
		"approve extends the active slot":      approveExtendsActive,
		"terminal transition frees the slot":   terminalFreesSlot,
		"work mirror follows transitions":      workMirror,
		"node reports":                         nodeReports,
		"delete work needs removed_at":         deleteWorkNeedsRemoval,
		"reapply clears removed_at":            reapplyClearsRemoval,
		"node ownership":                       nodeOwnership,
		"ip lease":                             ipLease,
		"health round trip":                    healthRoundTrip,
		"message bookkeeping":                  messageBookkeeping,
		"ending a session unsyncs its message": endingUnsyncsMessage,
	}
	for name, fn := range tests {
		t.Run(name, func(t *testing.T) { fn(t, mk(t)) })
	}
}

func createWritesBoth(t *testing.T, s Full) {
	require.NoError(t, s.CreateSession(ctx, pending("s1", "alice"), t0))

	got, err := s.GetSession(ctx, "s1")
	require.NoError(t, err)
	assert.Equal(t, store.StatusPending, got.Status)
	assert.Equal(t, "alice", got.User)

	w, err := s.GetWork(ctx, "s1")
	require.NoError(t, err)
	assert.Equal(t, store.StatusPending, w.Phase)
	assert.Equal(t, "PUB-s1", w.PublicKey)

	id, err := s.ActiveSessionID(ctx, "alice")
	require.NoError(t, err)
	assert.Equal(t, "s1", id)

	_, err = s.GetSession(ctx, "nope")
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func createLimits(t *testing.T, s Full) {
	require.NoError(t, s.CreateSession(ctx, pending("s1", "alice"), t0))
	assert.ErrorIs(t, s.CreateSession(ctx, pending("s2", "alice"), t0.Add(time.Minute)), store.ErrActive)

	require.NoError(t, s.ClearActive(ctx, "alice", "s1"))
	assert.NoError(t, s.CreateSession(ctx, pending("s2", "alice"), t0.Add(time.Minute)), "a freed slot allows a new request at once")

	// nothing from a rejected attempt may leak
	_, err := s.GetWork(ctx, "s3")
	assert.ErrorIs(t, err, store.ErrNotFound)
	assert.NoError(t, s.CreateSession(ctx, pending("s3", "bob"), t0), "other users are independent")
}

func transitionConflicts(t *testing.T, s Full) {
	require.NoError(t, s.CreateSession(ctx, pending("s1", "alice"), t0))

	require.NoError(t, s.Transition(ctx, "s1", []store.Status{store.StatusPending}, store.StatusApproved, store.Update{Approver: "bob"}))
	assert.ErrorIs(t, s.Transition(ctx, "s1", []store.Status{store.StatusPending}, store.StatusDenied, store.Update{}), store.ErrConflict)
	assert.ErrorIs(t, s.Transition(ctx, "nope", []store.Status{store.StatusPending}, store.StatusDenied, store.Update{}), store.ErrConflict)

	got, _ := s.GetSession(ctx, "s1")
	assert.Equal(t, store.StatusApproved, got.Status)
	assert.Equal(t, "bob", got.Approver)
}

func approveExtendsActive(t *testing.T, s Full) {
	require.NoError(t, s.CreateSession(ctx, pending("s1", "alice"), t0))
	exp := t0.Add(4 * time.Hour)
	require.NoError(t, s.Transition(ctx, "s1", []store.Status{store.StatusPending}, store.StatusApproved,
		store.Update{Address: "10.66.0.2", ExpiresAt: exp, ApprovedAt: t0.Add(time.Minute)}))

	// well past the pending deadline, the slot must still hold
	assert.ErrorIs(t, s.CreateSession(ctx, pending("s2", "alice"), t0.Add(time.Hour)), store.ErrActive)

	w, _ := s.GetWork(ctx, "s1")
	assert.Equal(t, exp, w.ExpiresAt)
	assert.Equal(t, "10.66.0.2", w.Address)
}

func terminalFreesSlot(t *testing.T, s Full) {
	require.NoError(t, s.CreateSession(ctx, pending("s1", "alice"), t0))
	require.NoError(t, s.Transition(ctx, "s1", []store.Status{store.StatusPending}, store.StatusDenied, store.Update{CloseReason: "denied"}))

	assert.NoError(t, s.CreateSession(ctx, pending("s2", "alice"), t0.Add(time.Second)))
	_, err := s.ActiveSessionID(ctx, "bob")
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func workMirror(t *testing.T, s Full) {
	require.NoError(t, s.CreateSession(ctx, pending("s1", "alice"), t0))
	require.NoError(t, s.Transition(ctx, "s1", []store.Status{store.StatusPending}, store.StatusApproved, store.Update{Address: "10.66.0.2", ExpiresAt: t0.Add(time.Hour)}))
	require.NoError(t, s.Transition(ctx, "s1", []store.Status{store.StatusApproved}, store.StatusClosed, store.Update{CloseReason: "idle"}))

	list, err := s.ListWork(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, store.StatusClosed, list[0].Phase)
	assert.Equal(t, "idle", list[0].CloseReason)
	assert.Equal(t, "alice", list[0].User)
}

func nodeReports(t *testing.T, s Full) {
	require.NoError(t, s.CreateSession(ctx, pending("s1", "alice"), t0))

	require.NoError(t, s.ReportApplied(ctx, "s1", "boot1", t0.Add(time.Minute)))
	require.NoError(t, s.ReportHandshake(ctx, "s1", t0.Add(2*time.Minute)))
	require.NoError(t, s.ReportError(ctx, "s1", "boom"))
	w, _ := s.GetWork(ctx, "s1")
	assert.Equal(t, "boot1", w.AppliedBootID)
	assert.Equal(t, t0.Add(2*time.Minute), w.LastHandshakeAt)
	assert.Equal(t, "boom", w.NodeError)

	assert.ErrorIs(t, s.ReportApplied(ctx, "nope", "boot1", t0), store.ErrNotFound)
}

func deleteWorkNeedsRemoval(t *testing.T, s Full) {
	require.NoError(t, s.CreateSession(ctx, pending("s1", "alice"), t0))
	assert.ErrorIs(t, s.DeleteWork(ctx, "s1"), store.ErrConflict)

	require.NoError(t, s.ReportRemoved(ctx, "s1", t0))
	require.NoError(t, s.DeleteWork(ctx, "s1"))
	assert.NoError(t, s.DeleteWork(ctx, "s1"), "idempotent")
	list, _ := s.ListWork(ctx)
	assert.Empty(t, list)
}

func reapplyClearsRemoval(t *testing.T, s Full) {
	require.NoError(t, s.CreateSession(ctx, pending("s1", "alice"), t0))
	require.NoError(t, s.ReportRemoved(ctx, "s1", t0))
	require.NoError(t, s.ReportApplied(ctx, "s1", "boot2", t0.Add(time.Minute)))

	w, _ := s.GetWork(ctx, "s1")
	assert.True(t, w.RemovedAt.IsZero())
}

func nodeOwnership(t *testing.T, s Full) {
	_, err := s.GetNode(ctx)
	assert.ErrorIs(t, err, store.ErrNotFound)

	a := store.Node{InstanceID: "i-a", BootID: "b1", PublicIP: "1.1.1.1", LastSeen: t0, State: "running", Mode: "normal"}
	require.NoError(t, s.PutNode(ctx, a, t0, 5*time.Minute))
	a.LastSeen = t0.Add(30 * time.Second)
	require.NoError(t, s.PutNode(ctx, a, t0.Add(30*time.Second), 5*time.Minute), "owner may update")

	other := store.Node{InstanceID: "i-b", BootID: "b2", LastSeen: t0.Add(time.Minute)}
	assert.ErrorIs(t, s.PutNode(ctx, other, t0.Add(time.Minute), 5*time.Minute), store.ErrConflict)
	require.NoError(t, s.PutNode(ctx, other, t0.Add(10*time.Minute), 5*time.Minute), "stale owner can be replaced")

	got, _ := s.GetNode(ctx)
	assert.Equal(t, "i-b", got.InstanceID)
}

func ipLease(t *testing.T, s Full) {
	require.NoError(t, s.LeaseIP(ctx, "10.66.0.2", "s1", time.Now().Add(time.Hour)))
	assert.ErrorIs(t, s.LeaseIP(ctx, "10.66.0.2", "s2", time.Now().Add(time.Hour)), store.ErrConflict)
	assert.ErrorIs(t, s.LeaseIP(ctx, "10.66.0.2", "s1", time.Now().Add(time.Hour)), store.ErrConflict, "even the holder cannot acquire it twice")
	assert.NoError(t, s.RenewLease(ctx, "10.66.0.2", "s1", time.Now().Add(2*time.Hour)))
	assert.ErrorIs(t, s.RenewLease(ctx, "10.66.0.2", "s2", time.Now().Add(2*time.Hour)), store.ErrConflict, "only the holder renews")
	assert.ErrorIs(t, s.RenewLease(ctx, "10.66.0.9", "s1", time.Now().Add(2*time.Hour)), store.ErrConflict, "nothing to renew")
	require.NoError(t, s.ReleaseIP(ctx, "10.66.0.2", "s2"))
	assert.ErrorIs(t, s.LeaseIP(ctx, "10.66.0.2", "s3", time.Now().Add(time.Hour)), store.ErrConflict, "a release by a non-holder is ignored")
	require.NoError(t, s.ReleaseIP(ctx, "10.66.0.2", "s1"))
	assert.NoError(t, s.LeaseIP(ctx, "10.66.0.2", "s2", time.Now().Add(time.Hour)))
}

func healthRoundTrip(t *testing.T, s Full) {
	h, err := s.GetHealth(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, h.StartFailures)

	require.NoError(t, s.PutHealth(ctx, store.Health{
		LastRun: t0, LastStartAt: t0.Add(time.Second), StartFailures: 2, CooldownUntil: t0.Add(time.Minute),
		RunningSince: t0.Add(2 * time.Minute), IdleSince: t0.Add(3 * time.Minute),
		UnresponsiveAlertedAt: t0.Add(4 * time.Minute), OverstayAlertedAt: t0.Add(5 * time.Minute),
	}))
	h, _ = s.GetHealth(ctx)
	assert.Equal(t, 2, h.StartFailures)
	assert.Equal(t, t0.Add(time.Second), h.LastStartAt)
	assert.Equal(t, t0.Add(time.Minute), h.CooldownUntil)
	assert.Equal(t, t0.Add(2*time.Minute), h.RunningSince)
	assert.Equal(t, t0.Add(3*time.Minute), h.IdleSince)
	assert.Equal(t, t0.Add(4*time.Minute), h.UnresponsiveAlertedAt)
	assert.Equal(t, t0.Add(5*time.Minute), h.OverstayAlertedAt)
}

func messageBookkeeping(t *testing.T, s Full) {
	require.NoError(t, s.CreateSession(ctx, pending("s1", "alice"), t0))
	require.NoError(t, s.SetMessageID(ctx, "s1", "m1"))
	require.NoError(t, s.SetMsgSynced(ctx, "s1", true))

	sess, _ := s.GetSession(ctx, "s1")
	w, _ := s.GetWork(ctx, "s1")
	assert.Equal(t, "m1", sess.DiscordMessageID)
	assert.Equal(t, "m1", w.DiscordMessageID)
	assert.True(t, sess.MsgSynced)
	assert.True(t, w.MsgSynced)
}

func endingUnsyncsMessage(t *testing.T, s Full) {
	require.NoError(t, s.CreateSession(ctx, pending("s1", "alice"), t0))
	require.NoError(t, s.SetMessageID(ctx, "s1", "m1"))
	require.NoError(t, s.Transition(ctx, "s1", []store.Status{store.StatusPending}, store.StatusApproved, store.Update{Address: "10.66.0.2/32", ApprovedAt: t0, ExpiresAt: t0.Add(time.Hour)}))
	require.NoError(t, s.SetMsgSynced(ctx, "s1", true))

	require.NoError(t, s.Transition(ctx, "s1", []store.Status{store.StatusApproved}, store.StatusClosed, store.Update{CloseReason: "user"}))

	sess, _ := s.GetSession(ctx, "s1")
	w, _ := s.GetWork(ctx, "s1")
	assert.False(t, sess.MsgSynced)
	assert.False(t, w.MsgSynced)
}
