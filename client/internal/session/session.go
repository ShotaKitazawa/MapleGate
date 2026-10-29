// Package session runs one request from asking for a tunnel to the end of it. It never touches the OS.
package session

import (
	"context"
	"fmt"
	"time"

	"github.com/ShotaKitazawa/MapleGate/client/internal/api"
)

type API interface {
	Create(ctx context.Context, publicKey string) (api.Session, error)
	Get(ctx context.Context, id string) (api.Session, error)
	Delete(ctx context.Context, id string) (api.Session, error)
}

type Runner struct {
	API API
	// Progress is told each new status and each poll that failed; it may be nil.
	Progress func(msg string)
	// Sleep waits between polls; tests replace it.
	Sleep func(context.Context, time.Duration) error
	// Poll is the interval while connected.
	Poll time.Duration
}

func (r *Runner) say(f string, a ...any) {
	if r.Progress != nil {
		r.Progress(fmt.Sprintf(f, a...))
	}
}

func (r *Runner) sleep(ctx context.Context, d time.Duration) error {
	if r.Sleep != nil {
		return r.Sleep(ctx, d)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// Request asks for a session and waits until it is active. A session left behind by an earlier run cannot be used
// because its key is gone, so it is closed and the request made again, once.
func (r *Runner) Request(ctx context.Context, publicKey string, wait time.Duration) (api.Session, error) {
	s, err := r.API.Create(ctx, publicKey)
	if err != nil {
		return s, err
	}
	if s.Conflict {
		r.say("closing the session %s left by an earlier run", s.ID)
		if _, err := r.API.Delete(ctx, s.ID); err != nil {
			return s, fmt.Errorf("close the earlier session: %w", err)
		}
		if s, err = r.API.Create(ctx, publicKey); err != nil {
			return s, err
		}
		if s.Conflict {
			return s, fmt.Errorf("another session %s is still open", s.ID)
		}
	}
	r.say("session %s: approve it in Discord", s.ID)

	deadline := time.Now().Add(wait)
	interval := 2 * time.Second
	last := ""
	for {
		if s.Status != last {
			r.say("status: %s", s.Status)
			last = s.Status
		}
		if s.Terminal() {
			return s, fmt.Errorf("session %s ended: %s %s", s.ID, s.Status, s.Error)
		}
		if s.Status == "active" && s.Config != nil {
			return s, nil
		}
		if time.Now().After(deadline) {
			return s, fmt.Errorf("session %s is still %s after %s", s.ID, s.Status, wait)
		}
		if err := r.sleep(ctx, interval); err != nil {
			return s, err
		}
		if interval < 10*time.Second {
			interval += time.Second
		}
		next, err := r.API.Get(ctx, s.ID)
		if err != nil {
			// a failed poll says nothing about the session
			r.say("poll failed: %v", err)
			continue
		}
		s = next
	}
}

// Watch returns when the session ends or ctx is done. A failed poll never ends it, so a dropped network cannot
// take down a tunnel that is still good.
func (r *Runner) Watch(ctx context.Context, id string) (api.Session, error) {
	every := r.Poll
	if every <= 0 {
		every = 30 * time.Second
	}
	for {
		if err := r.sleep(ctx, every); err != nil {
			return api.Session{}, err
		}
		s, err := r.API.Get(ctx, id)
		if err != nil {
			r.say("poll failed: %v", err)
			continue
		}
		if s.Terminal() {
			return s, nil
		}
	}
}

// Close ends the session at the server. It uses a fresh context so that a Ctrl-C does not cancel the call.
func (r *Runner) Close(id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_, err := r.API.Delete(ctx, id)
	return err
}
