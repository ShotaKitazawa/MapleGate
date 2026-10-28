package ipam

import (
	"context"
	"testing"
	"time"

	"github.com/ShotaKitazawa/MapleGate/internal/store"
	"github.com/stretchr/testify/assert"
)

func TestAllocateSkipsLeased(t *testing.T) {
	ctx := context.Background()
	m := store.NewMem()
	assert.NoError(t, m.LeaseIP(ctx, "10.66.0.2", "other", time.Time{}))

	got, err := Allocate(ctx, m, "10.66.0", "s1", time.Time{})
	assert.NoError(t, err)
	assert.Equal(t, "10.66.0.3", got)
}

func TestAllocateExhausted(t *testing.T) {
	ctx := context.Background()
	m := store.NewMem()
	for i := 2; i <= 254; i++ {
		_, err := Allocate(ctx, m, "10.66.0", "s", time.Time{})
		assert.NoError(t, err)
	}
	_, err := Allocate(ctx, m, "10.66.0", "s", time.Time{})
	assert.ErrorIs(t, err, ErrExhausted)
}
