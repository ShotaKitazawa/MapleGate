//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/ShotaKitazawa/MapleGate/internal/store"
	"github.com/stretchr/testify/require"
)

const boot = 120 * time.Second

func (c *cluster) activate(user string) (*client, status) {
	cl := c.create(user)
	c.press("approve", cl.sid)
	return cl, c.waitStatus(user, cl.sid, "active", boot)
}

func TestSessionLifecycleOverRealWireGuard(t *testing.T) {
	c := newCluster(t, nodeOpts{})

	cl, st := c.activate("alice")
	require.NoError(t, c.ping(cl, st), c.nodeLog())
	require.Equal(t, []string{cl.key.PublicKey().String()}, c.nodePeers())

	r := c.req("DELETE", "/session/"+cl.sid, "alice", "")
	require.Equal(t, 200, r.StatusCode, r.Body)
	c.eventually(30*time.Second, "the peer to be removed", func() bool { return len(c.nodePeers()) == 0 })
	c.eventually(30*time.Second, "the work item to be reclaimed", func() bool {
		_, err := c.st.GetWork(context.Background(), cl.sid)
		return err == store.ErrNotFound
	})
	s, err := c.st.GetSession(context.Background(), cl.sid)
	require.NoError(t, err)
	require.Equal(t, store.StatusClosed, s.Status)
	require.Error(t, c.ping(cl, st), "a closed session's key must stop working")
}

func TestDeniedRequestAppliesNothingAndTheNodeIdlesOut(t *testing.T) {
	c := newCluster(t, nodeOpts{bootGrace: "5s", idleStop: "10s"})

	cl := c.create("alice")
	c.eventually(boot, "the node to warm up while approval is pending", c.running)
	c.press("deny", cl.sid)
	c.waitStatus("alice", cl.sid, "denied", 10*time.Second)
	require.Empty(t, c.nodePeers())
	c.eventually(90*time.Second, "the node with nothing live to power off", func() bool { return !c.running() })
}

func TestIdleNodePowersItselfOffAndComesBack(t *testing.T) {
	c := newCluster(t, nodeOpts{bootGrace: "5s", idleStop: "10s"})

	cl, _ := c.activate("alice")
	r := c.req("DELETE", "/session/"+cl.sid, "alice", "")
	require.Equal(t, 200, r.StatusCode, r.Body)
	c.eventually(90*time.Second, "the idle node to power off", func() bool { return !c.running() })

	cl, st := c.activate("alice")
	require.NoError(t, c.ping(cl, st), c.nodeLog())
}

func TestNodeCrashIsHealedFromDynamoDB(t *testing.T) {
	c := newCluster(t, nodeOpts{})

	cl, st := c.activate("alice")
	require.NoError(t, c.ping(cl, st))

	// a hard stop gives the agent no chance to clean up, like an instance retirement
	out, err := ctr("stop", c.node)
	require.NoError(t, err, out)
	c.eventually(30*time.Second, "the janitor to restart the node", c.running)
	st = c.waitStatus("alice", cl.sid, "active", boot)
	require.NoError(t, c.ping(cl, st), c.nodeLog())
}

func TestBlindNodeKeepsExistingPeersAndCatchesUp(t *testing.T) {
	c := newCluster(t, nodeOpts{})

	alice, st := c.activate("alice")
	require.NoError(t, c.ping(alice, st))

	c.proxy.setDown(true)
	bob := c.create("bob")
	c.press("approve", bob.sid)
	time.Sleep(20 * time.Second)
	require.Equal(t, []string{alice.key.PublicKey().String()}, c.nodePeers(), "a blind node keeps what it has and adds nothing it cannot verify")
	require.NoError(t, c.ping(alice, st), "the established tunnel must survive the outage")

	c.proxy.setDown(false)
	stBob := c.waitStatus("bob", bob.sid, "active", boot)
	require.NoError(t, c.ping(bob, stBob), c.nodeLog())
	require.NoError(t, c.ping(alice, st))
}
