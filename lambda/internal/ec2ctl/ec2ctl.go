package ec2ctl

import "context"

type State string

const (
	StatePending      State = "pending"
	StateRunning      State = "running"
	StateStopping     State = "stopping"
	StateStopped      State = "stopped"
	StateTerminated   State = "terminated"
	StateShuttingDown State = "shutting-down"
	StateMissing      State = "missing"
)

// Controller abstracts the VPN node so handlers can be tested without AWS.
// It only starts the node and reads its state: peers are DynamoDB's business, not ours.
type Controller interface {
	// EnsureStarted starts the instance when it is stopped; it is a no-op otherwise.
	EnsureStarted(ctx context.Context) error
	State(ctx context.Context) (State, error)
}
