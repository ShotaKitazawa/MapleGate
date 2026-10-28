package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newClient(srv *httptest.Server) *Client {
	return &Client{
		Base: srv.URL, Region: "us-west-2", HTTP: srv.Client(),
		Creds: credentials.NewStaticCredentialsProvider("AKIDEXAMPLE", "secret", "token"),
		Now:   func() time.Time { return time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC) },
	}
}

func TestRequestsAreSignedForTheLambdaService(t *testing.T) {
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		w.WriteHeader(202)
		_, _ = w.Write([]byte(`{"session_id":"s1","status":"pending"}`))
	}))
	defer srv.Close()

	s, err := newClient(srv).Create(context.Background(), "PUB")
	require.NoError(t, err)

	assert.Equal(t, "s1", s.ID)
	auth := got.Header.Get("Authorization")
	assert.True(t, strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20261029/us-west-2/lambda/aws4_request"), auth)
	assert.Equal(t, "token", got.Header.Get("X-Amz-Security-Token"))
	assert.NotEmpty(t, got.Header.Get("X-Amz-Date"))
}

func TestConflictReturnsTheExistingSession(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(409)
		_, _ = w.Write([]byte(`{"session_id":"existing","status":"pending"}`))
	}))
	defer srv.Close()

	s, err := newClient(srv).Create(context.Background(), "PUB")
	require.NoError(t, err)
	assert.Equal(t, "existing", s.ID)
}

func TestForbiddenIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer srv.Close()

	_, err := newClient(srv).Get(context.Background(), "s1")
	var se *StatusError
	require.ErrorAs(t, err, &se)
	assert.Equal(t, 403, se.Code)
}

func TestOnlyEndedSessionsAreTerminal(t *testing.T) {
	for _, st := range []string{"denied", "failed", "closed", "expired"} {
		assert.True(t, Session{Status: st}.Terminal(), st)
	}
	for _, st := range []string{"pending", "provisioning", "active", ""} {
		assert.False(t, Session{Status: st}.Terminal(), st)
	}
}
