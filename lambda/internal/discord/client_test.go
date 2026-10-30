package discord

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRESTClient(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotAuth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"id":"m1"}`))
	}))
	defer srv.Close()
	c := &RESTClient{Token: func(context.Context) (string, error) { return "tok", nil }, HTTP: srv.Client(), Base: srv.URL}

	id, err := c.PostMessage(context.Background(), "ch", ApprovalMessage("s1", "alice"))
	assert.NoError(t, err)
	assert.Equal(t, "m1", id)
	assert.Equal(t, "POST /channels/ch/messages", gotMethod+" "+gotPath)
	assert.Equal(t, "Bot tok", gotAuth)

	assert.NoError(t, c.UpdateMessage(context.Background(), "ch", "m1", ResultMessage("s1", "alice", "approved", "bob", "t")))
	assert.Equal(t, "PATCH /channels/ch/messages/m1", gotMethod+" "+gotPath)
}

func TestApprovalMessageCustomIDs(t *testing.T) {
	m := ApprovalMessage("s1", "alice")
	btns := m.Components[0].Components
	assert.Equal(t, "approve:s1", btns[0].CustomID)
	assert.Equal(t, "deny:s1", btns[1].CustomID)
}

func TestRESTClientDropsTokenOn401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	dropped := 0
	c := &RESTClient{
		Token: func(context.Context) (string, error) { return "old", nil }, OnUnauthorized: func() { dropped++ },
		HTTP: srv.Client(), Base: srv.URL,
	}

	err := c.UpdateMessage(context.Background(), "ch", "m1", Message{})

	var api *APIError
	assert.ErrorAs(t, err, &api)
	assert.Equal(t, 401, api.Status)
	assert.Equal(t, 1, dropped)
}

func TestRepliesUseTheInteractionTokenAndNotTheBots(t *testing.T) {
	var method, path, auth, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		method, path, auth, body = r.Method, r.URL.Path, r.Header.Get("Authorization"), string(b)
	}))
	defer srv.Close()
	c := &RESTClient{Token: func(context.Context) (string, error) { return "tok", nil }, HTTP: srv.Client(), Base: srv.URL}

	assert.NoError(t, c.Followup(context.Background(), "app", "itok", "more"))
	assert.Equal(t, "POST /webhooks/app/itok", method+" "+path)
	assert.Empty(t, auth)
	assert.JSONEq(t, `{"content":"more","flags":64}`, body, "the second message is private too")
}
