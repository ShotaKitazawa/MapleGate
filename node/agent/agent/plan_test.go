package agent

import (
	"encoding/base64"
	"net/netip"
	"testing"
	"time"

	"github.com/ShotaKitazawa/MapleGate/internal/store"
	"github.com/ShotaKitazawa/MapleGate/node/agent/wg"
	"github.com/stretchr/testify/assert"
)

var (
	now    = time.Unix(1_000_000, 0)
	subnet = netip.MustParsePrefix("10.66.0.0/24")
)

func key(n byte) string {
	b := make([]byte, 32)
	b[0] = n
	return base64.StdEncoding.EncodeToString(b)
}

func approved(sid string, n byte) store.Work {
	return store.Work{
		SID: sid, Phase: store.StatusApproved, PublicKey: key(n), Address: "10.66.0." + string(rune('0'+n)),
		ExpiresAt: now.Add(time.Hour),
	}
}

func plan(in PlanInput) []Op {
	in.Subnet, in.Now, in.BootID, in.HandshakeEvery = subnet, now, "boot1", time.Minute
	return Plan(in)
}

func kinds(ops []Op) []OpKind {
	var k []OpKind
	for _, o := range ops {
		k = append(k, o.Kind)
	}
	return k
}

func TestPlanAddsMissingPeerAndReportsIt(t *testing.T) {
	ops := plan(PlanInput{Work: []store.Work{approved("s1", 2)}})
	assert.Equal(t, []OpKind{OpAdd, OpReportApplied}, kinds(ops))
	assert.Equal(t, key(2), ops[0].PublicKey)
	assert.Equal(t, "10.66.0.2", ops[0].Address)
}

func TestPlanIsQuietWhenConverged(t *testing.T) {
	w := approved("s1", 2)
	w.AppliedBootID = "boot1"
	ops := plan(PlanInput{Work: []store.Work{w}, Peers: []wg.Peer{{PublicKey: w.PublicKey, Address: w.Address}}})
	assert.Empty(t, ops)
}

func TestPlanReportsAppliedAfterReboot(t *testing.T) {
	w := approved("s1", 2)
	w.AppliedBootID = "old-boot"
	ops := plan(PlanInput{Work: []store.Work{w}, Peers: []wg.Peer{{PublicKey: w.PublicKey, Address: w.Address}}})
	assert.Equal(t, []OpKind{OpReportApplied}, kinds(ops))
}

func TestPlanSkipsPending(t *testing.T) {
	w := approved("s1", 2)
	w.Phase = store.StatusPending
	assert.Empty(t, plan(PlanInput{Work: []store.Work{w}}))
}

func TestPlanRemovesTerminalAndConfirms(t *testing.T) {
	w := approved("s1", 2)
	w.Phase = store.StatusClosed
	ops := plan(PlanInput{Work: []store.Work{w}, Peers: []wg.Peer{{PublicKey: w.PublicKey, Address: w.Address}}})
	assert.Equal(t, []OpKind{OpRemove, OpReportRemoved}, kinds(ops))

	again := plan(PlanInput{Work: []store.Work{w}})
	assert.Equal(t, []OpKind{OpReportRemoved}, kinds(again), "peer already gone: only the confirmation is missing")

	w.RemovedAt = now
	assert.Empty(t, plan(PlanInput{Work: []store.Work{w}}))
}

func TestPlanRemovesExpiredApprovedWithoutWaitingForTheJanitor(t *testing.T) {
	w := approved("s1", 2)
	w.ExpiresAt = now.Add(-time.Second)
	ops := plan(PlanInput{Work: []store.Work{w}, Peers: []wg.Peer{{PublicKey: w.PublicKey, Address: w.Address}}})
	assert.Equal(t, []OpKind{OpRemove, OpReportRemoved}, kinds(ops))
}

func TestPlanRemovesUnknownPeers(t *testing.T) {
	ops := plan(PlanInput{Peers: []wg.Peer{{PublicKey: key(9), Address: "10.66.0.9"}}})
	assert.Equal(t, []OpKind{OpRemove}, kinds(ops))
}

func TestPlanDoesNotTreatAbsenceAsEvidence(t *testing.T) {
	p := wg.Peer{PublicKey: key(2), Address: "10.66.0.2"}
	k := Known{SID: "s1", PublicKey: key(2), Address: "10.66.0.2", ExpiresAt: now.Add(time.Hour)}

	assert.Empty(t, plan(PlanInput{Peers: []wg.Peer{p}, Known: []Known{k}}), "an empty queue must not wipe peers we applied")

	k.ExpiresAt = now.Add(-time.Second)
	assert.Equal(t, []OpKind{OpRemove}, kinds(plan(PlanInput{Peers: []wg.Peer{p}, Known: []Known{k}})), "but our own expiry still applies")
}

func TestPlanRejectsInvalidItems(t *testing.T) {
	cases := map[string]func(*store.Work){
		"invalid public key":             func(w *store.Work) { w.PublicKey = "nope" },
		"address outside the VPN subnet": func(w *store.Work) { w.Address = "192.168.1.5" },
		"address is reserved":            func(w *store.Work) { w.Address = "10.66.0.1" },
	}
	for want, mutate := range cases {
		w := approved("s1", 2)
		mutate(&w)
		ops := plan(PlanInput{Work: []store.Work{w}})
		assert.Equal(t, []OpKind{OpReportError}, kinds(ops), want)
		assert.Equal(t, want, ops[0].Message)

		w.NodeError = want
		assert.Empty(t, plan(PlanInput{Work: []store.Work{w}}), "already reported: no write per tick")
	}
}

func TestPlanRejectsDuplicatesAndOverflow(t *testing.T) {
	a, b := approved("s1", 2), approved("s2", 3)
	b.Address = a.Address
	ops := plan(PlanInput{Work: []store.Work{a, b}})
	assert.ElementsMatch(t, []OpKind{OpAdd, OpReportApplied, OpReportError}, kinds(ops))

	c, d := approved("s1", 2), approved("s2", 3)
	ops = plan(PlanInput{Work: []store.Work{c, d}, MaxPeers: 1})
	assert.Contains(t, kinds(ops), OpReportError)
	assert.Equal(t, 1, count(ops, OpAdd))
}

func count(ops []Op, k OpKind) int {
	n := 0
	for _, o := range ops {
		if o.Kind == k {
			n++
		}
	}
	return n
}

func TestPlanThrottlesHandshakeReports(t *testing.T) {
	w := approved("s1", 2)
	w.AppliedBootID = "boot1"
	w.LastHandshakeAt = now.Add(-30 * time.Second)
	peer := wg.Peer{PublicKey: w.PublicKey, Address: w.Address, LastHandshake: now}
	assert.Empty(t, plan(PlanInput{Work: []store.Work{w}, Peers: []wg.Peer{peer}}))

	w.LastHandshakeAt = now.Add(-2 * time.Minute)
	assert.Equal(t, []OpKind{OpReportShake}, kinds(plan(PlanInput{Work: []store.Work{w}, Peers: []wg.Peer{peer}})))
}

func TestPlanChangesBeforeReports(t *testing.T) {
	gone := approved("s1", 2)
	gone.Phase = store.StatusClosed
	add := approved("s2", 3)
	ops := plan(PlanInput{
		Work:  []store.Work{add, gone},
		Peers: []wg.Peer{{PublicKey: gone.PublicKey, Address: gone.Address}},
	})
	assert.Equal(t, []OpKind{OpRemove, OpAdd, OpReportApplied, OpReportRemoved}, kinds(ops))
}
