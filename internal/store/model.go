package store

import (
	"context"
	"errors"
	"time"
)

type Status string

const (
	StatusPending  Status = "pending"
	StatusApproved Status = "approved"
	StatusDenied   Status = "denied"
	StatusFailed   Status = "failed"
	StatusClosed   Status = "closed"
	StatusExpired  Status = "expired"
)

func (s Status) Terminal() bool {
	switch s {
	case StatusDenied, StatusFailed, StatusClosed, StatusExpired:
		return true
	}
	return false
}

// Live sessions keep the node up: someone is waiting for it or using it.
func (s Status) Live() bool { return s == StatusPending || s == StatusApproved }

var (
	ErrNotFound    = errors.New("not found")
	ErrConflict    = errors.New("conflict")
	ErrRateLimited = errors.New("rate limited")
	ErrActive      = errors.New("user already has an active session")
)

type Session struct {
	ID               string
	User             string
	PublicKey        string
	Status           Status
	Address          string
	DiscordMessageID string
	MsgSynced        bool
	Approver         string
	CloseReason      string
	Error            string
	CreatedAt        time.Time
	ApprovedAt       time.Time
	ExpiresAt        time.Time
}

// Work is the queue item both sides act on. Lambdas own the intent half and the node owns the observation half.
type Work struct {
	SID              string
	User             string
	Phase            Status
	PublicKey        string
	Address          string
	ExpiresAt        time.Time
	ApprovedAt       time.Time
	DiscordMessageID string
	MsgSynced        bool
	CloseReason      string

	AppliedAt       time.Time
	AppliedBootID   string
	LastHandshakeAt time.Time
	RemovedAt       time.Time
	NodeError       string
}

type Node struct {
	InstanceID string
	BootID     string
	PublicIP   string
	LastSeen   time.Time
	State      string
	Mode       string
}

type Health struct {
	LastRun       time.Time
	LastStartAt   time.Time
	StartFailures int
	CooldownUntil time.Time
}

// Update lists the fields Transition may change; zero values are left untouched.
// A non-zero ExpiresAt also extends the user's active-session slot.
type Update struct {
	Address         string
	Approver        string
	Error           string
	CloseReason     string
	ExpiresAt       time.Time
	ApprovedAt      time.Time
	RefundRateLimit bool
}

// Store is the Lambda side. Terminal transitions free the user's active slot but never the IP lease.
type Store interface {
	// CreateSession writes META, WORK, the active slot and the rate limit atomically.
	CreateSession(ctx context.Context, s Session, now time.Time, rateLimit time.Duration) error
	GetSession(ctx context.Context, id string) (Session, error)
	ActiveSessionID(ctx context.Context, user string) (string, error)
	SetMessageID(ctx context.Context, id, messageID string) error
	SetMsgSynced(ctx context.Context, id string, synced bool) error
	// Transition fails with ErrConflict unless the current status is one of from.
	Transition(ctx context.Context, id string, from []Status, to Status, u Update) error
	ClearActive(ctx context.Context, user, sid string) error

	// LeaseIP fails with ErrConflict when addr is already leased.
	LeaseIP(ctx context.Context, addr, sid string, until time.Time) error
	// ReleaseIP frees addr only while sid still holds it, so a repeated release cannot take a newer lease.
	ReleaseIP(ctx context.Context, addr, sid string) error

	ListWork(ctx context.Context) ([]Work, error)
	GetWork(ctx context.Context, sid string) (Work, error)
	// MarkRemoved is the janitor's one write to the node's half, allowed only while the node is stopped.
	MarkRemoved(ctx context.Context, sid string, at time.Time) error
	// DeleteWork fails with ErrConflict until removed_at is set.
	DeleteWork(ctx context.Context, sid string) error

	GetNode(ctx context.Context) (Node, error)
	GetHealth(ctx context.Context) (Health, error)
	PutHealth(ctx context.Context, h Health) error
}

// NodeStore is the agent side; it cannot touch sessions, leases or health.
type NodeStore interface {
	ListWork(ctx context.Context) ([]Work, error)
	GetNode(ctx context.Context) (Node, error)
	// PutNode fails with ErrConflict while a different instance has been seen within takeoverAfter.
	PutNode(ctx context.Context, n Node, now time.Time, takeoverAfter time.Duration) error
	ReportApplied(ctx context.Context, sid, bootID string, at time.Time) error
	ReportRemoved(ctx context.Context, sid string, at time.Time) error
	ReportHandshake(ctx context.Context, sid string, at time.Time) error
	ReportError(ctx context.Context, sid, msg string) error
}

// Visible maps the stored status and the node's observations to what the API reports.
func Visible(s Status, w Work, n Node, now time.Time, stale time.Duration) string {
	if s != StatusApproved {
		return string(s)
	}
	if w.AppliedBootID != "" && w.AppliedBootID == n.BootID && now.Sub(n.LastSeen) <= stale {
		return "active"
	}
	return "provisioning"
}

func contains(list []Status, s Status) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
