package ipam

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ShotaKitazawa/MapleGate/internal/store"
)

var ErrExhausted = errors.New("no free address")

type Leaser interface {
	LeaseIP(ctx context.Context, addr, sid string, until time.Time) error
}

// Allocate leases the first free host address in prefix (e.g. "10.66.0") from .2;
// .1 is the server. The conditional put in LeaseIP prevents double allocation.
func Allocate(ctx context.Context, l Leaser, prefix, sid string, until time.Time) (string, error) {
	for host := 2; host <= 254; host++ {
		addr := fmt.Sprintf("%s.%d", prefix, host)
		err := l.LeaseIP(ctx, addr, sid, until)
		if err == nil {
			return addr, nil
		}
		if !errors.Is(err, store.ErrConflict) {
			return "", err
		}
	}
	return "", ErrExhausted
}
