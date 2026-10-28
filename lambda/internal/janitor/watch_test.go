package janitor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ShotaKitazawa/MapleGate/internal/store"
	"github.com/ShotaKitazawa/MapleGate/lambda/internal/ec2ctl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// minutes advances the clock a minute at a time, with the node reporting until it is told to stay silent.
func (e *env) minutes(t *testing.T, n int, nodeReports bool) {
	for i := 0; i < n; i++ {
		e.now = e.now.Add(time.Minute)
		if nodeReports {
			e.nodeUp(t, "boot1")
			e.useApprovedPeers(t)
		}
		e.run(t)
	}
}

// useApprovedPeers plays the node applying a session and its owner keeping the tunnel busy.
func (e *env) useApprovedPeers(t *testing.T) {
	works, err := e.st.ListWork(ctx)
	require.NoError(t, err)
	for _, w := range works {
		if w.Phase != store.StatusApproved {
			continue
		}
		if w.AppliedAt.IsZero() {
			require.NoError(t, e.st.ReportApplied(ctx, w.SID, "boot1", e.now))
		}
		require.NoError(t, e.st.ReportHandshake(ctx, w.SID, e.now))
	}
}

func TestAHealthyBusyNodeRaisesNoAlert(t *testing.T) {
	e := newEnv()
	e.approved(t, "s1", "alice", "10.66.0.2")
	e.minutes(t, 90, true)

	assert.Empty(t, e.dc.posts)
}

func TestANodeThatStopsReportingIsAnnouncedOncePerHour(t *testing.T) {
	e := newEnv()
	e.nodeUp(t, "boot1")
	e.run(t)

	e.minutes(t, 10, false)
	assert.Empty(t, e.dc.posts, "silence is only counted once the grace period is over")
	e.minutes(t, 2, false)
	require.Len(t, e.dc.posts, 1)
	assert.Contains(t, e.dc.posts[0], "i-1")
	assert.Contains(t, e.dc.posts[0], "not reported")

	e.minutes(t, 50, false)
	assert.Len(t, e.dc.posts, 1, "no repeat inside the hour")
	e.minutes(t, 15, false)
	assert.Len(t, e.dc.posts, 2, "a node left hung is mentioned again")
}

func TestAFreshlyBootedNodeIsNotMistakenForAHungOne(t *testing.T) {
	e := newEnv()
	e.nodeUp(t, "old")
	e.ec2.state = ec2ctl.StateStopped
	e.minutes(t, 120, false)

	e.ec2.state = ec2ctl.StateRunning
	e.minutes(t, 5, false)

	assert.Empty(t, e.dc.posts, "the heartbeat left by the previous run is old by definition")
}

func TestARecoveredNodeThatHangsAgainIsAnnouncedAtOnce(t *testing.T) {
	e := newEnv()
	e.nodeUp(t, "boot1")
	e.run(t)
	e.minutes(t, 15, false)
	require.Len(t, e.dc.posts, 1)

	e.minutes(t, 3, true)
	e.minutes(t, 12, false)

	assert.Len(t, e.dc.posts, 2, "a new incident does not wait out the repeat interval")
}

func TestANodeThatShouldHaveStoppedIsAnnounced(t *testing.T) {
	e := newEnv()
	e.minutes(t, 35, true)
	assert.Empty(t, e.dc.posts, "the agent stops an idle node after the boot grace plus the idle stop")

	e.minutes(t, 3, true)
	require.Len(t, e.dc.posts, 1)
	assert.Contains(t, e.dc.posts[0], "no live session")
}

func TestALiveSessionRestartsTheIdleClock(t *testing.T) {
	e := newEnv()
	e.minutes(t, 30, true)
	e.approved(t, "s1", "alice", "10.66.0.2")
	e.minutes(t, 20, true)
	require.NoError(t, e.st.Transition(ctx, "s1", []store.Status{store.StatusApproved}, store.StatusClosed, store.Update{CloseReason: "user"}))

	e.minutes(t, 30, true)

	assert.Empty(t, e.dc.posts)
}

func TestARequestAwaitingApprovalDoesNotHoldOffTheOverstayAlert(t *testing.T) {
	e := newEnv()
	e.minutes(t, 20, true)
	e.pending(t, "s1", "alice")
	e.minutes(t, 20, true)

	assert.NotEmpty(t, e.dc.posts, "the node should have stopped; a request nobody answered is no reason to keep it")
}

func TestAStoppedNodeRaisesNothingAndForgetsItsHistory(t *testing.T) {
	e := newEnv()
	e.nodeUp(t, "boot1")
	e.minutes(t, 60, false)
	require.NotEmpty(t, e.dc.posts)
	e.dc.posts = nil

	e.ec2.state = ec2ctl.StateStopped
	e.minutes(t, 5, false)
	h, err := e.st.GetHealth(ctx)
	require.NoError(t, err)

	assert.Empty(t, e.dc.posts)
	assert.True(t, h.RunningSince.IsZero() && h.IdleSince.IsZero() && h.UnresponsiveAlertedAt.IsZero())
}

func TestAFailedAlertIsRetriedOnTheNextRun(t *testing.T) {
	e := newEnv()
	e.nodeUp(t, "boot1")
	e.run(t)
	e.dc.postErr = errors.New("discord down")
	e.minutes(t, 15, false)
	assert.Empty(t, e.dc.posts)

	e.dc.postErr = nil
	e.minutes(t, 1, false)

	assert.Len(t, e.dc.posts, 1)
}

func TestAnUnknownInstanceStateRaisesNothing(t *testing.T) {
	e := newEnv()
	e.ec2.state = ""
	e.minutes(t, 90, false)

	assert.Empty(t, e.dc.posts)
}

func (e *env) expiresAt(t *testing.T, sid string) time.Time {
	s, err := e.st.GetSession(ctx, sid)
	require.NoError(t, err)
	w, err := e.st.GetWork(ctx, sid)
	require.NoError(t, err)
	require.Equal(t, s.ExpiresAt, w.ExpiresAt, "META and WORK must always agree on the deadline")
	return s.ExpiresAt
}

func TestASessionInUseOutlivesItsTTL(t *testing.T) {
	e := newEnv()
	e.approved(t, "s1", "alice", "10.66.0.2")
	first := e.expiresAt(t, "s1")

	e.minutes(t, 7*60, true)

	assert.Equal(t, store.StatusApproved, e.status(t, "s1"), "seven hours of use against a 4 hour TTL")
	assert.True(t, e.expiresAt(t, "s1").After(first.Add(5*time.Hour)), "the deadline moved with the use")
	assert.True(t, e.expiresAt(t, "s1").After(e.now.Add(3*time.Hour)), "and it is never close to ending while in use")
	id, err := e.st.ActiveSessionID(ctx, "alice")
	require.NoError(t, err)
	assert.Equal(t, "s1", id)
}

func TestASessionIsNotExtendedUntilItNeedsIt(t *testing.T) {
	e := newEnv()
	e.approved(t, "s1", "alice", "10.66.0.2")
	first := e.expiresAt(t, "s1")

	e.minutes(t, 20, true)

	assert.Equal(t, first, e.expiresAt(t, "s1"), "writes are saved until the deadline is within the extension window")
}

func TestASessionIsNotExtendedWhileTheNodeIsSilent(t *testing.T) {
	e := newEnv()
	e.approved(t, "s1", "alice", "10.66.0.2")
	e.minutes(t, 60, true)
	first := e.expiresAt(t, "s1")

	e.minutes(t, 5*60, false)

	assert.Equal(t, store.StatusExpired, e.status(t, "s1"), "with nobody to vouch for it the TTL ends it")
	assert.True(t, first.Before(e.now))
}

// leaseSpy records renewals: the in-memory store has no lease clock, so only the call itself can be observed here.
type leaseSpy struct {
	*store.Mem
	renewed map[string][]time.Time
}

func (s *leaseSpy) RenewLease(c context.Context, addr, sid string, until time.Time) error {
	s.renewed[addr] = append(s.renewed[addr], until)
	return s.Mem.RenewLease(c, addr, sid, until)
}

func TestAnExtensionRenewsTheLeaseOfTheAddress(t *testing.T) {
	e := newEnv()
	spy := &leaseSpy{Mem: e.st, renewed: map[string][]time.Time{}}
	e.j.Store = spy
	e.approved(t, "s1", "alice", "10.66.0.2")

	e.minutes(t, 5*60, true)

	require.NotEmpty(t, spy.renewed["10.66.0.2"], "an address whose lease is not renewed is up for grabs once the original one runs out")
	assert.True(t, spy.renewed["10.66.0.2"][len(spy.renewed["10.66.0.2"])-1].After(e.now.Add(3*time.Hour)))
	assert.ErrorIs(t, e.st.LeaseIP(ctx, "10.66.0.2", "other", e.now.Add(time.Hour)), store.ErrConflict)
}
