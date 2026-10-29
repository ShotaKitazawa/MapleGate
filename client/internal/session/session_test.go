package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ShotaKitazawa/MapleGate/client/internal/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fake struct {
	creates   []api.Session
	gets      []any
	deleted   []string
	deleteErr error
}

func (f *fake) Create(context.Context, string) (api.Session, error) {
	s := f.creates[0]
	f.creates = f.creates[1:]
	return s, nil
}

func (f *fake) Get(context.Context, string) (api.Session, error) {
	g := f.gets[0]
	if len(f.gets) > 1 {
		f.gets = f.gets[1:]
	}
	if err, ok := g.(error); ok {
		return api.Session{}, err
	}
	return g.(api.Session), nil
}

func (f *fake) Delete(_ context.Context, id string) (api.Session, error) {
	f.deleted = append(f.deleted, id)
	return api.Session{}, f.deleteErr
}

func runner(f *fake) *Runner {
	return &Runner{API: f, Sleep: func(ctx context.Context, _ time.Duration) error { return ctx.Err() }, Poll: time.Millisecond}
}

var active = api.Session{ID: "s", Status: "active", Config: &api.Config{Address: "10.66.0.2/32"}}

func TestRequestWaitsThroughPendingAndProvisioningUntilActive(t *testing.T) {
	f := &fake{creates: []api.Session{{ID: "s", Status: "pending"}},
		gets: []any{api.Session{ID: "s", Status: "pending"}, api.Session{ID: "s", Status: "provisioning"}, active}}

	s, err := runner(f).Request(context.Background(), "PUB", time.Minute)

	require.NoError(t, err)
	assert.Equal(t, "10.66.0.2/32", s.Config.Address)
}

func TestRequestFailsWhenTheSessionEndsFirst(t *testing.T) {
	f := &fake{creates: []api.Session{{ID: "s", Status: "pending"}}, gets: []any{api.Session{ID: "s", Status: "denied"}}}

	_, err := runner(f).Request(context.Background(), "PUB", time.Minute)

	assert.ErrorContains(t, err, "denied")
}

func TestRequestIgnoresAFailedPoll(t *testing.T) {
	f := &fake{creates: []api.Session{{ID: "s", Status: "pending"}}, gets: []any{errors.New("network"), active}}

	_, err := runner(f).Request(context.Background(), "PUB", time.Minute)

	require.NoError(t, err)
}

func TestRequestClosesASessionLeftByAnEarlierRunAndAsksAgain(t *testing.T) {
	f := &fake{creates: []api.Session{{ID: "old", Status: "approved", Conflict: true}, active}}

	s, err := runner(f).Request(context.Background(), "PUB", time.Minute)

	require.NoError(t, err)
	assert.Equal(t, []string{"old"}, f.deleted)
	assert.Equal(t, "s", s.ID)
}

func TestRequestGivesUpWhenTheLeftSessionCannotBeClosed(t *testing.T) {
	f := &fake{creates: []api.Session{{ID: "old", Status: "approved", Conflict: true}}, deleteErr: errors.New("boom")}

	_, err := runner(f).Request(context.Background(), "PUB", time.Minute)

	assert.ErrorContains(t, err, "close the earlier session")
}

func TestRequestStopsWaitingAtItsDeadline(t *testing.T) {
	f := &fake{creates: []api.Session{{ID: "s", Status: "pending"}}, gets: []any{api.Session{ID: "s", Status: "pending"}}}

	_, err := runner(f).Request(context.Background(), "PUB", -time.Second)

	assert.ErrorContains(t, err, "still pending")
}

func TestWatchReturnsOnlyWhenTheSessionHasEnded(t *testing.T) {
	f := &fake{gets: []any{active, errors.New("network"), api.Session{ID: "s", Status: "provisioning"}, api.Session{ID: "s", Status: "closed"}}}

	s, err := runner(f).Watch(context.Background(), "s")

	require.NoError(t, err)
	assert.Equal(t, "closed", s.Status)
}

func TestWatchStopsWhenTheContextIsDone(t *testing.T) {
	f := &fake{gets: []any{active}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := runner(f).Watch(ctx, "s")

	assert.ErrorIs(t, err, context.Canceled)
}
