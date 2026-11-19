package store

import (
	"context"
	"log/slog"
	"slices"
	"time"
)

// Audit logs when a session is approved and when it ends. Every such change goes through Transition, so one wrapper
// covers the session API, the Discord handler and the janitor, where a log line per call site would miss the next one.
type Audit struct {
	Store
}

func (a Audit) Transition(ctx context.Context, id string, from []Status, to Status, u Update) error {
	if err := a.Store.Transition(ctx, id, from, to, u); err != nil {
		return err
	}
	switch {
	case to.Terminal():
		a.log(ctx, "session ended", id)
	case to == StatusApproved && slices.Contains(from, StatusPending):
		a.log(ctx, "session approved", id)
	}
	return nil
}

func (a Audit) log(ctx context.Context, msg, id string) {
	s, err := a.Store.GetSession(ctx, id)
	if err != nil {
		slog.Warn(msg, "session", id, "err", err)
		return
	}
	attrs := []any{
		"session", s.ID, "user", s.User, "status", string(s.Status), "reason", s.CloseReason,
		"approver", s.Approver, "created_at", s.CreatedAt, "approved_at", s.ApprovedAt, "expires_at", s.ExpiresAt,
	}
	if s.Error != "" {
		attrs = append(attrs, "error", s.Error)
	}
	// the end of a session is not stamped, so its length is told from when the log line is written
	if s.Status.Terminal() && !s.ApprovedAt.IsZero() {
		attrs = append(attrs, "held_seconds", int(time.Since(s.ApprovedAt).Seconds()))
	}
	slog.Info(msg, attrs...)
}
