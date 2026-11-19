package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/ShotaKitazawa/MapleGate/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func captureLogs(t *testing.T) func() []map[string]any {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	return func() []map[string]any {
		var out []map[string]any
		for _, l := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
			if l == "" {
				continue
			}
			var m map[string]any
			require.NoError(t, json.Unmarshal([]byte(l), &m))
			out = append(out, m)
		}
		return out
	}
}

func TestAuditLogsTheApprovalAndTheEndOfASession(t *testing.T) {
	logs := captureLogs(t)
	ctx := context.Background()
	now := time.Now()
	st := store.Audit{Store: store.NewMem()}
	require.NoError(t, st.CreateSession(ctx, store.Session{ID: "s1", User: "alice", Status: store.StatusPending, CreatedAt: now, ExpiresAt: now.Add(15 * time.Minute)}, now))

	require.NoError(t, st.Transition(ctx, "s1", []store.Status{store.StatusPending}, store.StatusApproved, store.Update{Approver: "bob", ApprovedAt: now, ExpiresAt: now.Add(4 * time.Hour)}))
	require.NoError(t, st.Transition(ctx, "s1", []store.Status{store.StatusApproved}, store.StatusApproved, store.Update{ExpiresAt: now.Add(5 * time.Hour)}))
	require.NoError(t, st.Transition(ctx, "s1", []store.Status{store.StatusApproved}, store.StatusClosed, store.Update{CloseReason: "idle"}))

	got := logs()
	require.Len(t, got, 2, "an extension is not an event")
	assert.Equal(t, "session approved", got[0]["msg"])
	assert.Equal(t, "bob", got[0]["approver"])
	assert.Equal(t, "session ended", got[1]["msg"])
	assert.Equal(t, "alice", got[1]["user"])
	assert.Equal(t, "closed", got[1]["status"])
	assert.Equal(t, "idle", got[1]["reason"])
	assert.Contains(t, got[1], "held_seconds")
}

func TestAuditLogsNothingForATransitionThatFailed(t *testing.T) {
	logs := captureLogs(t)
	st := store.Audit{Store: store.NewMem()}

	err := st.Transition(context.Background(), "nope", []store.Status{store.StatusPending}, store.StatusDenied, store.Update{})

	assert.ErrorIs(t, err, store.ErrConflict)
	assert.Empty(t, logs())
}
