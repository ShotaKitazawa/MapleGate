package store_test

import (
	"testing"
	"time"

	"github.com/ShotaKitazawa/MapleGate/internal/store"
	"github.com/ShotaKitazawa/MapleGate/internal/store/storetest"
	"github.com/stretchr/testify/assert"
)

func TestMemContract(t *testing.T) {
	storetest.Run(t, func(*testing.T) storetest.Full { return store.NewMem() })
}

func TestVisible(t *testing.T) {
	now := time.Unix(1000, 0)
	node := store.Node{BootID: "b1", LastSeen: now.Add(-30 * time.Second)}
	applied := store.Work{AppliedBootID: "b1"}
	stale := 2 * time.Minute

	assert.Equal(t, "active", store.Visible(store.StatusApproved, applied, node, now, stale))
	assert.Equal(t, "provisioning", store.Visible(store.StatusApproved, store.Work{}, node, now, stale), "not applied yet")
	assert.Equal(t, "provisioning", store.Visible(store.StatusApproved, store.Work{AppliedBootID: "old"}, node, now, stale), "node rebooted")
	node.LastSeen = now.Add(-10 * time.Minute)
	assert.Equal(t, "provisioning", store.Visible(store.StatusApproved, applied, node, now, stale), "heartbeat stale")
	assert.Equal(t, "pending", store.Visible(store.StatusPending, applied, node, now, stale))
	assert.Equal(t, "closed", store.Visible(store.StatusClosed, applied, node, now, stale))
}
