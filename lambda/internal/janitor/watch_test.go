package janitor

import (
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
