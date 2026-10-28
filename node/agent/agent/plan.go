package agent

import (
	"encoding/base64"
	"fmt"
	"net/netip"
	"sort"
	"time"

	"github.com/ShotaKitazawa/MapleGate/internal/store"
	"github.com/ShotaKitazawa/MapleGate/node/agent/wg"
)

type OpKind string

const (
	OpAdd           OpKind = "add"
	OpRemove        OpKind = "remove"
	OpReportApplied OpKind = "applied"
	OpReportRemoved OpKind = "removed"
	OpReportShake   OpKind = "handshake"
	OpReportError   OpKind = "error"
)

type Op struct {
	Kind      OpKind
	SID       string
	PublicKey string
	Address   string
	At        time.Time
	Message   string
}

// Known is a peer this agent applied and still owes a removal for, restored from the local cache after a restart.
type Known struct {
	SID       string    `json:"sid"`
	PublicKey string    `json:"public_key"`
	Address   string    `json:"address"`
	ExpiresAt time.Time `json:"expires_at"`
	AppliedAt time.Time `json:"applied_at"`
}

type PlanInput struct {
	Work     []store.Work
	Peers    []wg.Peer
	Known    []Known
	BootID   string
	Subnet   netip.Prefix
	Now      time.Time
	MaxPeers int
	// HandshakeEvery throttles handshake reports so the table's write capacity is not spent on them.
	HandshakeEvery time.Duration
}

// Plan computes what to do to make the kernel peers match the work queue.
// Absence of a work item is never a reason to remove a peer we applied (I7); an unknown peer is.
func Plan(in PlanInput) []Op {
	var ops []Op
	peers := map[string]wg.Peer{}
	for _, p := range in.Peers {
		peers[p.PublicKey] = p
	}
	known := map[string]Known{}
	for _, k := range in.Known {
		known[k.PublicKey] = k
	}

	desired, rejected := selectDesired(in)
	current := map[string]string{}
	for _, w := range in.Work {
		current[w.SID] = w.NodeError
	}
	for sid, msg := range rejected {
		if current[sid] != msg {
			ops = append(ops, Op{Kind: OpReportError, SID: sid, Message: msg})
		}
	}

	byKey := map[string]store.Work{}
	for _, w := range in.Work {
		byKey[w.PublicKey] = w
	}

	for _, w := range desired {
		p, present := peers[w.PublicKey]
		if !present || p.Address != w.Address {
			ops = append(ops, Op{Kind: OpAdd, SID: w.SID, PublicKey: w.PublicKey, Address: w.Address})
			ops = append(ops, Op{Kind: OpReportApplied, SID: w.SID, At: in.Now})
			continue
		}
		if w.AppliedBootID != in.BootID {
			ops = append(ops, Op{Kind: OpReportApplied, SID: w.SID, At: in.Now})
		}
		if !p.LastHandshake.IsZero() && p.LastHandshake.Sub(w.LastHandshakeAt) >= in.HandshakeEvery {
			ops = append(ops, Op{Kind: OpReportShake, SID: w.SID, At: p.LastHandshake})
		}
	}

	wanted := map[string]bool{}
	for _, w := range desired {
		wanted[w.PublicKey] = true
	}
	removed := map[string]bool{}
	for _, p := range in.Peers {
		if wanted[p.PublicKey] {
			continue
		}
		w, hasItem := byKey[p.PublicKey]
		k, wasKnown := known[p.PublicKey]
		switch {
		case hasItem:
			// the item says it must not exist: terminal, expired or invalid
		case wasKnown && in.Now.Before(k.ExpiresAt):
			continue
		case wasKnown:
		default:
			// unknown peer: not ours
		}
		ops = append(ops, Op{Kind: OpRemove, SID: w.SID, PublicKey: p.PublicKey})
		removed[p.PublicKey] = true
	}

	for _, w := range in.Work {
		if wanted[w.PublicKey] || w.Phase == store.StatusPending || !w.RemovedAt.IsZero() {
			continue
		}
		if _, still := peers[w.PublicKey]; still && !removed[w.PublicKey] {
			continue
		}
		if _, bad := rejected[w.SID]; bad {
			continue
		}
		ops = append(ops, Op{Kind: OpReportRemoved, SID: w.SID, At: in.Now})
	}

	sort.SliceStable(ops, func(i, j int) bool { return rank(ops[i].Kind) < rank(ops[j].Kind) })
	return ops
}

// rank puts kernel changes before reports so a report never claims what has not happened.
func rank(k OpKind) int {
	switch k {
	case OpRemove:
		return 0
	case OpAdd:
		return 1
	}
	return 2
}

func selectDesired(in PlanInput) (desired []store.Work, rejected map[string]string) {
	rejected = map[string]string{}
	seenKey, seenAddr := map[string]string{}, map[string]string{}
	for _, w := range in.Work {
		if w.Phase != store.StatusApproved || !in.Now.Before(w.ExpiresAt) {
			continue
		}
		if err := validate(w, in.Subnet); err != nil {
			rejected[w.SID] = err.Error()
			continue
		}
		if other, dup := seenKey[w.PublicKey]; dup {
			rejected[w.SID] = "duplicate public key with " + other
			continue
		}
		if other, dup := seenAddr[w.Address]; dup {
			rejected[w.SID] = "duplicate address with " + other
			continue
		}
		if in.MaxPeers > 0 && len(desired) >= in.MaxPeers {
			rejected[w.SID] = "too many peers"
			continue
		}
		seenKey[w.PublicKey], seenAddr[w.Address] = w.SID, w.SID
		desired = append(desired, w)
	}
	return desired, rejected
}

func validate(w store.Work, subnet netip.Prefix) error {
	raw, err := base64.StdEncoding.DecodeString(w.PublicKey)
	if err != nil || len(raw) != 32 {
		return fmt.Errorf("invalid public key")
	}
	a, err := netip.ParseAddr(w.Address)
	if err != nil || !subnet.Contains(a) {
		return fmt.Errorf("address outside the VPN subnet")
	}
	if a.Is4() {
		switch a.As4()[3] {
		case 0, 1, 255:
			return fmt.Errorf("address is reserved")
		}
	}
	return nil
}
