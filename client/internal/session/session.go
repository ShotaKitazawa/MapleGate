// Package session runs one request from asking for a tunnel to the end of it. It never touches the OS.
package session

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ShotaKitazawa/MapleGate/client/internal/api"
	"github.com/ShotaKitazawa/MapleGate/client/internal/auth"
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
	// OnLapse is told when Watch can no longer poll because the sign in has expired, and again once it can; it may be nil.
	OnLapse func(lapsed bool)
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

// EndedError is a request that reached an end before the tunnel could be brought up.
type EndedError struct{ ID, Status, Detail string }

func (e *EndedError) Error() string {
	return fmt.Sprintf("session %s ended: %s %s", e.ID, e.Status, e.Detail)
}

// ConflictError is a request refused because the account already has a live session.
type ConflictError struct{ ID, Status string }

func (e *ConflictError) Error() string {
	return fmt.Sprintf("session %s (%s) is already open", e.ID, e.Status)
}

// WaitError is a request that was still not active when the wait ran out.
type WaitError struct {
	ID, Status string
	Wait       time.Duration
}

func (e *WaitError) Error() string {
	return fmt.Sprintf("session %s is still %s after %s", e.ID, e.Status, e.Wait)
}

func describe(status string) string {
	switch status {
	case "pending":
		return "Waiting for approval in Discord"
	case "provisioning":
		return "Approved, starting the VPN node"
	}
	return "status: " + status
}

// Request asks for a session and waits until it is active. A session that already exists is never closed here: it may
// be in use from another device, and one left by a crash ends by itself, which costs a wait but cuts nobody off.
func (r *Runner) Request(ctx context.Context, publicKey string, wait time.Duration) (api.Session, error) {
	s, err := r.API.Create(ctx, publicKey)
	if err != nil {
		return s, err
	}
	if s.Conflict {
		return s, &ConflictError{ID: s.ID, Status: s.Status}
	}
	r.say("session %s", s.ID)

	deadline := time.Now().Add(wait)
	interval := 2 * time.Second
	last := ""
	for {
		if s.Status != last {
			r.say("%s", describe(s.Status))
			last = s.Status
		}
		if s.Terminal() {
			return s, &EndedError{ID: s.ID, Status: s.Status, Detail: s.Error}
		}
		if s.Status == "active" && s.Config != nil {
			return s, nil
		}
		if time.Now().After(deadline) {
			return s, &WaitError{ID: s.ID, Status: s.Status, Wait: wait}
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
	ctx = auth.Quiet(ctx)
	every := r.Poll
	if every <= 0 {
		every = 30 * time.Second
	}
	lapsed := false
	lapse := func(v bool) {
		if v != lapsed && r.OnLapse != nil {
			r.OnLapse(v)
		}
		lapsed = v
	}
	for {
		if err := r.sleep(ctx, every); err != nil {
			return api.Session{}, err
		}
		s, err := r.API.Get(ctx, id)
		if errors.Is(err, auth.ErrSignInRequired) {
			lapse(true)
			continue
		}
		if err != nil {
			r.say("poll failed: %v", err)
			continue
		}
		lapse(false)
		if s.Terminal() {
			return s, nil
		}
	}
}

// Close ends the session at the server. It uses a fresh context so that a Ctrl-C does not cancel the call.
func (r *Runner) Close(id string) error {
	ctx, cancel := context.WithTimeout(auth.Quiet(context.Background()), 8*time.Second)
	defer cancel()
	_, err := r.API.Delete(ctx, id)
	return err
}
