package janitor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ShotaKitazawa/MapleGate/internal/store"
	"github.com/ShotaKitazawa/MapleGate/lambda/internal/discord"
	"github.com/ShotaKitazawa/MapleGate/lambda/internal/ec2ctl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var ctx = context.Background()

type fakeEC2 struct {
	state   ec2ctl.State
	started int
	err     error
}

func (f *fakeEC2) EnsureStarted(context.Context) error { f.started++; return f.err }
func (f *fakeEC2) State(context.Context) (ec2ctl.State, error) {
	return f.state, nil
}

type fakeDiscord struct {
	updates int
	err     error
	posts   []string
	postErr error
}

func (f *fakeDiscord) PostMessage(_ context.Context, _ string, m discord.Message) (string, error) {
	if f.postErr != nil {
		return "", f.postErr
	}
	f.posts = append(f.posts, m.Content)
	return "m", nil
}
func (f *fakeDiscord) UpdateMessage(context.Context, string, string, discord.Message) error {
	if f.err != nil {
		return f.err
	}
	f.updates++
	return nil
}

type env struct {
	j   *Janitor
	st  *store.Mem
	ec2 *fakeEC2
	dc  *fakeDiscord
	now time.Time
}

func newEnv() *env {
	e := &env{st: store.NewMem(), ec2: &fakeEC2{state: ec2ctl.StateRunning}, dc: &fakeDiscord{}, now: time.Unix(1_000_000, 0)}
	e.j = &Janitor{
		Cfg: Config{
			ProvisionTimeout: 5 * time.Minute, HandshakeIdle: 10 * time.Minute, StaleAfter: 2 * time.Minute,
			MsgSyncGiveUp: time.Hour, StartCooldown: 15 * time.Minute, BreakerLimit: 2,
			SessionTTL: 4 * time.Hour, ExtendEvery: 30 * time.Minute, InstanceID: "i-1", UnresponsiveAfter: 10 * time.Minute, OverstayAfter: 35 * time.Minute, AlertRepeat: time.Hour,
		},
		Store: e.st, EC2: e.ec2, Discord: e.dc,
		ChannelID: func(context.Context) (string, error) { return "ch", nil },
		Now:       func() time.Time { return e.now },
	}
	return e
}

func (e *env) run(t *testing.T) { require.NoError(t, e.j.Run(ctx)) }

func (e *env) pending(t *testing.T, sid, user string) {
	require.NoError(t, e.st.CreateSession(ctx, store.Session{
		ID: sid, User: user, PublicKey: "PUB", Status: store.StatusPending, CreatedAt: e.now, ExpiresAt: e.now.Add(15 * time.Minute),
	}, e.now))
	require.NoError(t, e.st.SetMessageID(ctx, sid, "m-"+sid))
}

func (e *env) approved(t *testing.T, sid, user, addr string) {
	e.pending(t, sid, user)
	require.NoError(t, e.st.LeaseIP(ctx, addr, sid, e.now.Add(time.Hour)))
	require.NoError(t, e.st.Transition(ctx, sid, []store.Status{store.StatusPending}, store.StatusApproved,
		store.Update{Address: addr, Approver: "bob", ExpiresAt: e.now.Add(4 * time.Hour), ApprovedAt: e.now}))
}

func (e *env) nodeUp(t *testing.T, boot string) {
	require.NoError(t, e.st.PutNode(ctx, store.Node{InstanceID: "i-1", BootID: boot, PublicIP: "1.2.3.4", LastSeen: e.now}, e.now, time.Minute))
}

func (e *env) status(t *testing.T, sid string) store.Status {
	s, err := e.st.GetSession(ctx, sid)
	require.NoError(t, err)
	return s.Status
}

func (e *env) hasWork(sid string) bool {
	_, err := e.st.GetWork(ctx, sid)
	return err == nil
}

func TestPendingPastDeadlineExpiresAndIsFullyReclaimedWhileNodeIsStopped(t *testing.T) {
	e := newEnv()
	e.ec2.state = ec2ctl.StateStopped
	e.pending(t, "s1", "alice")
	e.now = e.now.Add(20 * time.Minute)

	e.run(t)

	assert.Equal(t, store.StatusExpired, e.status(t, "s1"))
	assert.False(t, e.hasWork("s1"), "no peer can exist on a stopped node, so the janitor confirms removal")
	assert.Equal(t, 1, e.dc.updates)
	assert.Equal(t, 0, e.ec2.started, "nothing live remains")
}

func TestExpiredApprovedWaitsForTheNodeToConfirmRemoval(t *testing.T) {
	e := newEnv()
	e.approved(t, "s1", "alice", "10.66.0.2")
	e.nodeUp(t, "b1")
	e.now = e.now.Add(5 * time.Hour)

	e.run(t)
	assert.Equal(t, store.StatusExpired, e.status(t, "s1"))
	assert.True(t, e.hasWork("s1"), "the peer may still exist")
	assert.ErrorIs(t, e.st.LeaseIP(ctx, "10.66.0.2", "x", e.now), store.ErrConflict, "I6: the lease must outlive the peer")

	require.NoError(t, e.st.ReportRemoved(ctx, "s1", e.now))
	e.run(t)
	assert.False(t, e.hasWork("s1"))
	assert.NoError(t, e.st.LeaseIP(ctx, "10.66.0.2", "x", e.now), "released after confirmation")
}

func TestRepeatedRunsDoNotStealANewerLease(t *testing.T) {
	e := newEnv()
	e.approved(t, "s1", "alice", "10.66.0.2")
	e.ec2.state = ec2ctl.StateStopped
	e.now = e.now.Add(5 * time.Hour)
	e.run(t)
	require.NoError(t, e.st.LeaseIP(ctx, "10.66.0.2", "s2", e.now.Add(time.Hour)))

	e.run(t)
	e.run(t)

	assert.ErrorIs(t, e.st.LeaseIP(ctx, "10.66.0.2", "s3", e.now), store.ErrConflict, "s2 keeps its lease")
}

func TestApprovedNeverAppliedFails(t *testing.T) {
	e := newEnv()
	e.approved(t, "s1", "alice", "10.66.0.2")
	e.nodeUp(t, "b1")

	e.now = e.now.Add(4 * time.Minute)
	e.nodeUp(t, "b1")
	e.run(t)
	assert.Equal(t, store.StatusApproved, e.status(t, "s1"))

	e.now = e.now.Add(2 * time.Minute)
	e.run(t)
	assert.Equal(t, store.StatusFailed, e.status(t, "s1"))
}

func TestIdleHandshakeClosesTheSession(t *testing.T) {
	e := newEnv()
	e.approved(t, "s1", "alice", "10.66.0.2")
	e.nodeUp(t, "b1")
	require.NoError(t, e.st.ReportApplied(ctx, "s1", "b1", e.now))

	e.now = e.now.Add(9 * time.Minute)
	e.nodeUp(t, "b1")
	e.run(t)
	assert.Equal(t, store.StatusApproved, e.status(t, "s1"))

	require.NoError(t, e.st.ReportHandshake(ctx, "s1", e.now))
	e.now = e.now.Add(9 * time.Minute)
	e.nodeUp(t, "b1")
	e.run(t)
	assert.Equal(t, store.StatusApproved, e.status(t, "s1"), "a recent handshake resets the idle clock")

	e.now = e.now.Add(2 * time.Minute)
	e.nodeUp(t, "b1")
	e.run(t)
	assert.Equal(t, store.StatusClosed, e.status(t, "s1"))
	s, _ := e.st.GetSession(ctx, "s1")
	assert.Equal(t, "idle", s.CloseReason)
}

func TestNoIdleJudgementWhileTheNodeIsNotReportingForTheCurrentBoot(t *testing.T) {
	e := newEnv()
	e.approved(t, "s1", "alice", "10.66.0.2")
	e.nodeUp(t, "b1")
	require.NoError(t, e.st.ReportApplied(ctx, "s1", "b1", e.now))

	e.now = e.now.Add(30 * time.Minute)
	e.nodeUp(t, "b2")
	e.run(t)

	assert.Equal(t, store.StatusApproved, e.status(t, "s1"), "after a reboot the peer is not yet re-applied, which is not the user's idleness")
}

func TestApprovedSessionStartsAStoppedNode(t *testing.T) {
	e := newEnv()
	e.ec2.state = ec2ctl.StateStopped
	e.approved(t, "s1", "alice", "10.66.0.2")

	e.run(t)
	assert.Equal(t, 1, e.ec2.started)

	e.ec2.state = ec2ctl.StateRunning
	e.run(t)
	assert.Equal(t, 1, e.ec2.started, "running nodes are left alone")
}

func TestARequestStillAwaitingApprovalDoesNotStartTheNode(t *testing.T) {
	e := newEnv()
	e.ec2.state = ec2ctl.StateStopped
	e.pending(t, "s1", "alice")

	e.run(t)
	e.now = e.now.Add(5 * time.Minute)
	e.run(t)
	assert.Equal(t, 0, e.ec2.started, "a refusal or an unanswered request must not cost a start")

	require.NoError(t, e.st.LeaseIP(ctx, "10.66.0.2", "s1", e.now.Add(time.Hour)))
	require.NoError(t, e.st.Transition(ctx, "s1", []store.Status{store.StatusPending}, store.StatusApproved,
		store.Update{Address: "10.66.0.2", Approver: "bob", ExpiresAt: e.now.Add(4 * time.Hour), ApprovedAt: e.now}))
	e.run(t)
	assert.Equal(t, 1, e.ec2.started, "approval is what starts it")
}

func TestBreakerStopsARestartLoop(t *testing.T) {
	e := newEnv()
	e.ec2.state = ec2ctl.StateStopped
	e.approved(t, "s1", "alice", "10.66.0.2")

	e.run(t)
	e.now = e.now.Add(time.Minute)
	e.run(t)
	assert.Equal(t, 2, e.ec2.started, "two starts without any heartbeat")

	e.now = e.now.Add(time.Minute)
	e.run(t)
	assert.Equal(t, 2, e.ec2.started)
	assert.Equal(t, store.StatusFailed, e.status(t, "s1"))
	s, _ := e.st.GetSession(ctx, "s1")
	assert.Contains(t, s.Error, "did not come up")

	e.approved(t, "s2", "bob", "10.66.0.3")
	e.now = e.now.Add(time.Minute)
	e.run(t)
	assert.Equal(t, 2, e.ec2.started, "cooldown")

	e.now = e.now.Add(20 * time.Minute)
	e.approved(t, "s3", "carol", "10.66.0.4")
	e.run(t)
	assert.Equal(t, 3, e.ec2.started, "cooldown over")
}

func TestStartErrorsCountTowardTheBreaker(t *testing.T) {
	e := newEnv()
	e.ec2.state = ec2ctl.StateStopped
	e.ec2.err = errors.New("InsufficientInstanceCapacity")
	e.approved(t, "s1", "alice", "10.66.0.2")

	e.run(t)
	e.now = e.now.Add(time.Minute)
	e.run(t)
	e.now = e.now.Add(time.Minute)
	e.run(t)

	assert.Equal(t, store.StatusFailed, e.status(t, "s1"))
}

func TestMissingInstanceFailsLiveSessions(t *testing.T) {
	e := newEnv()
	e.ec2.state = ec2ctl.StateMissing
	e.pending(t, "s1", "alice")

	e.run(t)

	assert.Equal(t, store.StatusFailed, e.status(t, "s1"))
	assert.Equal(t, 0, e.ec2.started)
}

func TestMessageSyncIsRetriedThenGivenUp(t *testing.T) {
	e := newEnv()
	e.ec2.state = ec2ctl.StateStopped
	e.approved(t, "s1", "alice", "10.66.0.2")
	e.dc.err = errors.New("discord 500")
	e.now = e.now.Add(5 * time.Hour)

	e.run(t)
	assert.True(t, e.hasWork("s1"), "kept until the message is in sync")

	e.dc.err = nil
	e.run(t)
	assert.False(t, e.hasWork("s1"))
	assert.Equal(t, 1, e.dc.updates)
}

func TestMessageGoneIsNotRetriedForever(t *testing.T) {
	e := newEnv()
	e.ec2.state = ec2ctl.StateStopped
	e.approved(t, "s1", "alice", "10.66.0.2")
	e.dc.err = &discord.APIError{Status: 404}
	e.now = e.now.Add(5 * time.Hour)

	e.run(t)

	assert.False(t, e.hasWork("s1"))
}

func TestOrphanWorkIsReclaimed(t *testing.T) {
	e := newEnv()
	e.ec2.state = ec2ctl.StateStopped
	require.NoError(t, e.st.CreateSession(ctx, store.Session{ID: "s1", User: "alice", Status: store.StatusPending, ExpiresAt: e.now.Add(time.Hour)}, e.now))
	e.st.DropSession("s1")

	e.run(t)

	assert.False(t, e.hasWork("s1"))
}

func TestRunWritesHealth(t *testing.T) {
	e := newEnv()
	e.run(t)
	h, _ := e.st.GetHealth(ctx)
	assert.Equal(t, e.now, h.LastRun)
}
