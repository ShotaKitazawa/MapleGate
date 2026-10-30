package interactions

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ShotaKitazawa/MapleGate/internal/store"
	"github.com/ShotaKitazawa/MapleGate/lambda/internal/config"
	"github.com/ShotaKitazawa/MapleGate/lambda/internal/discord"
	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeDiscord struct {
	updates []discord.Message
	err     error
}

func (f *fakeDiscord) PostMessage(context.Context, string, discord.Message) (string, error) {
	return "m", nil
}
func (f *fakeDiscord) UpdateMessage(_ context.Context, _, _ string, m discord.Message) error {
	if f.err != nil {
		return f.err
	}
	f.updates = append(f.updates, m)
	return nil
}

type fakeInvoker struct {
	payloads [][]byte
	err      error
}

func (f *fakeInvoker) InvokeAsync(_ context.Context, p []byte) error {
	f.payloads = append(f.payloads, p)
	return f.err
}

type env struct {
	h    *Handler
	st   *store.Mem
	dc   *fakeDiscord
	inv  *fakeInvoker
	priv ed25519.PrivateKey
	now  time.Time
}

const signedAt = "1700000000"

func newEnv(t *testing.T) *env {
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	e := &env{st: store.NewMem(), dc: &fakeDiscord{}, inv: &fakeInvoker{}, priv: priv, now: time.Unix(1_700_000_000, 0)}
	e.h = &Handler{
		Cfg:       config.Config{WGPort: 51820, WGSubnet: "10.66.0", SessionTTL: 4 * time.Hour},
		Store:     e.st,
		Discord:   e.dc,
		Invoker:   e.inv,
		ChannelID: func(context.Context) (string, error) { return "ch", nil },
		PublicKey: func(context.Context) (ed25519.PublicKey, error) { return pub, nil },
		Now:       func() time.Time { return e.now },
	}
	return e
}

func (e *env) seedPending(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, e.st.CreateSession(ctx, store.Session{
		ID: "s1", User: "alice", PublicKey: "PUB", Status: store.StatusPending, ExpiresAt: e.now.Add(10 * time.Minute),
	}, e.now))
	require.NoError(t, e.st.SetMessageID(ctx, "s1", "m1"))
}

func (e *env) signed(body string) events.LambdaFunctionURLRequest {
	sig := hex.EncodeToString(ed25519.Sign(e.priv, append([]byte(signedAt), body...)))
	return events.LambdaFunctionURLRequest{
		Body:    body,
		Headers: map[string]string{"x-signature-ed25519": sig, "x-signature-timestamp": signedAt},
	}
}

func buttonBody(customID string) string {
	return `{"type":3,"data":{"custom_id":"` + customID + `"},"member":{"user":{"id":"u9","username":"bob","global_name":"Bob"}},"message":{"id":"m1"}}`
}

func TestRejectsBadSignature(t *testing.T) {
	e := newEnv(t)
	r := e.signed(`{"type":1}`)
	r.Headers["x-signature-ed25519"] = "00"
	assert.Equal(t, 401, e.h.Handle(context.Background(), r).StatusCode)

	r.Headers = nil
	assert.Equal(t, 401, e.h.Handle(context.Background(), r).StatusCode)
}

func TestRejectsStaleTimestamp(t *testing.T) {
	e := newEnv(t)
	r := e.signed(`{"type":1}`)
	e.now = e.now.Add(10 * time.Minute)
	assert.Equal(t, 401, e.h.Handle(context.Background(), r).StatusCode, "a captured request must not stay replayable")
}

func TestPing(t *testing.T) {
	e := newEnv(t)
	r := e.h.Handle(context.Background(), e.signed(`{"type":1}`))
	assert.Equal(t, 200, r.StatusCode)
	assert.JSONEq(t, `{"type":1}`, r.Body)
}

func commandBody(name string) string {
	return `{"type":2,"data":{"name":"` + name + `"},"member":{"user":{"id":"u9","username":"bob"}}}`
}

type fakeReplies struct {
	edits, followups []string
	editErr          error
}

func (f *fakeReplies) EditOriginal(_ context.Context, app, tok, content string) error {
	f.edits = append(f.edits, app+"/"+tok+": "+content)
	return f.editErr
}
func (f *fakeReplies) Followup(_ context.Context, app, tok, content string) error {
	f.followups = append(f.followups, app+"/"+tok+": "+content)
	return nil
}

func downloadEnv(t *testing.T, url string, err error) (*env, *fakeReplies) {
	e := newEnv(t)
	r := &fakeReplies{}
	e.h.Replies = r
	e.h.DownloadURL = func(context.Context, string) (string, error) { return url, err }
	return e, r
}

func commandBodyFor(name string) string {
	return `{"type":2,"application_id":"app1","token":"tok1","data":{"name":"` + name + `"},"member":{"user":{"id":"u9","username":"bob"}}}`
}

// run delivers the event the command handed to the async invoke
func (e *env) runDownload(t *testing.T) {
	require.Len(t, e.inv.payloads, 1)
	var ev AsyncEvent
	require.NoError(t, json.Unmarshal(e.inv.payloads[0], &ev))
	require.NoError(t, e.h.HandleAsync(context.Background(), ev))
}

func TestDownloadCommandAcknowledgesPrivatelyAndHandsTheRestOn(t *testing.T) {
	e, _ := downloadEnv(t, "https://example.test/x", nil)

	r := e.h.Handle(context.Background(), e.signed(commandBodyFor("download-macos")))

	assert.JSONEq(t, `{"type":5,"data":{"flags":64}}`, r.Body, "deferred, and ephemeral from the start")
	var ev AsyncEvent
	require.NoError(t, json.Unmarshal(e.inv.payloads[0], &ev))
	assert.Equal(t, AsyncEvent{Action: "download", Platform: "macos", AppID: "app1", Token: "tok1"}, ev)
}

func TestDownloadSendsTheLinkAndThenTheHintsOfThatSystem(t *testing.T) {
	e, r := downloadEnv(t, "https://example.test/pkg?sig=x", nil)

	e.h.Handle(context.Background(), e.signed(commandBodyFor("download-macos")))
	e.runDownload(t)

	require.Len(t, r.edits, 1)
	assert.Equal(t, "app1/tok1: [Download link](https://example.test/pkg?sig=x) (works for 15 minutes)", r.edits[0], "the long URL stays out of sight")
	require.Len(t, r.followups, 1)
	assert.Contains(t, r.followups[0], "Open Anyway")
	assert.Contains(t, r.followups[0], "Disconnect")
	assert.NotContains(t, r.followups[0], "More info")
}

func TestDownloadHintsForWindows(t *testing.T) {
	e, r := downloadEnv(t, "https://example.test/x", nil)

	e.h.Handle(context.Background(), e.signed(commandBodyFor("download-windows")))
	e.runDownload(t)

	assert.Contains(t, r.followups[0], "Run anyway")
	assert.NotContains(t, r.followups[0], "Open Anyway")
}

func TestDownloadWhenTheLinkCannotBeMadeSaysSoWithoutTheCause(t *testing.T) {
	e, r := downloadEnv(t, "", errors.New("boom"))

	e.h.Handle(context.Background(), e.signed(commandBodyFor("download-macos")))
	e.runDownload(t)

	require.Len(t, r.edits, 1)
	assert.NotContains(t, r.edits[0], "boom")
	assert.Contains(t, r.edits[0], "could not be made")
	assert.Empty(t, r.followups, "hints without a link would be noise")
}

func TestDownloadForASystemWithoutInstaller(t *testing.T) {
	e, r := downloadEnv(t, "", nil)

	e.h.Handle(context.Background(), e.signed(commandBodyFor("download-windows")))
	e.runDownload(t)

	assert.Contains(t, r.edits[0], "no installer for windows")
	assert.Empty(t, r.followups)
}

func TestDownloadWhenTheHandOverFailsTellsThePersonNow(t *testing.T) {
	e, _ := downloadEnv(t, "https://example.test/x", nil)
	e.inv.err = errors.New("boom")

	r := e.h.Handle(context.Background(), e.signed(commandBodyFor("download-macos")))

	assert.Contains(t, r.Body, `"type":4`)
	assert.Contains(t, r.Body, "Try again")
}

func TestUnknownCommand(t *testing.T) {
	e := newEnv(t)
	assert.Equal(t, 400, e.h.Handle(context.Background(), e.signed(commandBody("other"))).StatusCode)
}

func TestButtonDefersAndInvokesSelf(t *testing.T) {
	e := newEnv(t)
	r := e.h.Handle(context.Background(), e.signed(buttonBody("approve:s1")))

	assert.JSONEq(t, `{"type":6}`, r.Body)
	require.Len(t, e.inv.payloads, 1)
	var ev AsyncEvent
	require.NoError(t, json.Unmarshal(e.inv.payloads[0], &ev))
	assert.Equal(t, AsyncEvent{Action: "approve", SessionID: "s1", Approver: Approver{ID: "u9", Name: "Bob"}}, ev)
}

func TestButtonInvalidCustomID(t *testing.T) {
	e := newEnv(t)
	assert.Equal(t, 400, e.h.Handle(context.Background(), e.signed(buttonBody("other:s1"))).StatusCode)
	assert.Empty(t, e.inv.payloads)
}

func TestButtonInvokeFailure(t *testing.T) {
	e := newEnv(t)
	e.inv.err = errors.New("boom")
	assert.Equal(t, 500, e.h.Handle(context.Background(), e.signed(buttonBody("approve:s1"))).StatusCode, "the clicker sees a failure and can press again")
}

func TestApproveRecordsIntentOnly(t *testing.T) {
	e := newEnv(t)
	e.seedPending(t)
	ev := AsyncEvent{Action: "approve", SessionID: "s1", Approver: Approver{Name: "Bob"}}

	require.NoError(t, e.h.HandleAsync(context.Background(), ev))

	s, _ := e.st.GetSession(context.Background(), "s1")
	assert.Equal(t, store.StatusApproved, s.Status)
	assert.Equal(t, "10.66.0.2", s.Address)
	assert.Equal(t, "Bob", s.Approver)
	assert.Equal(t, e.now.Add(4*time.Hour), s.ExpiresAt)
	w, _ := e.st.GetWork(context.Background(), "s1")
	assert.Equal(t, store.StatusApproved, w.Phase)
	assert.Equal(t, "10.66.0.2", w.Address, "the node reads the address from WORK")
	assert.True(t, w.AppliedAt.IsZero(), "applying is the node's job")
	require.Len(t, e.dc.updates, 1)
	assert.Contains(t, e.dc.updates[0].Content, "approved")
	assert.Contains(t, e.dc.updates[0].Content, "Bob")
	assert.Empty(t, e.dc.updates[0].Components, "buttons must go away")
}

func TestDoublePressApprovesOnce(t *testing.T) {
	e := newEnv(t)
	e.seedPending(t)
	ev := AsyncEvent{Action: "approve", SessionID: "s1", Approver: Approver{Name: "Bob"}}

	require.NoError(t, e.h.HandleAsync(context.Background(), ev))
	require.NoError(t, e.h.HandleAsync(context.Background(), ev))
	require.NoError(t, e.h.HandleAsync(context.Background(), AsyncEvent{Action: "deny", SessionID: "s1"}))

	s, _ := e.st.GetSession(context.Background(), "s1")
	assert.Equal(t, store.StatusApproved, s.Status)
	assert.Equal(t, "10.66.0.2", s.Address)
	assert.NoError(t, e.st.LeaseIP(context.Background(), "10.66.0.3", "x", time.Time{}), "the losing presses must not leak a lease")
}

func TestDeny(t *testing.T) {
	e := newEnv(t)
	e.seedPending(t)

	require.NoError(t, e.h.HandleAsync(context.Background(), AsyncEvent{Action: "deny", SessionID: "s1", Approver: Approver{Name: "Bob"}}))

	s, _ := e.st.GetSession(context.Background(), "s1")
	assert.Equal(t, store.StatusDenied, s.Status)
	require.Len(t, e.dc.updates, 1)
	assert.Contains(t, e.dc.updates[0].Content, "denied")
	assert.NoError(t, e.st.CreateSession(context.Background(), store.Session{ID: "s2", User: "alice", ExpiresAt: e.now.Add(time.Hour)}, e.now), "active slot must be freed")
}

func TestApproveAfterPendingDeadline(t *testing.T) {
	e := newEnv(t)
	e.seedPending(t)
	e.now = e.now.Add(time.Hour)

	require.NoError(t, e.h.HandleAsync(context.Background(), AsyncEvent{Action: "approve", SessionID: "s1"}))

	s, _ := e.st.GetSession(context.Background(), "s1")
	assert.Equal(t, store.StatusExpired, s.Status)
}

func TestPressOnDecidedSessionRefreshesMessage(t *testing.T) {
	e := newEnv(t)
	e.seedPending(t)
	require.NoError(t, e.st.Transition(context.Background(), "s1", []store.Status{store.StatusPending}, store.StatusClosed, store.Update{}))

	require.NoError(t, e.h.HandleAsync(context.Background(), AsyncEvent{Action: "approve", SessionID: "s1"}))

	require.Len(t, e.dc.updates, 1)
	assert.Contains(t, e.dc.updates[0].Content, "closed")
}

func TestMessageUpdateFailureLeavesItForTheJanitor(t *testing.T) {
	e := newEnv(t)
	e.seedPending(t)
	e.dc.err = errors.New("discord 500")

	require.NoError(t, e.h.HandleAsync(context.Background(), AsyncEvent{Action: "approve", SessionID: "s1"}))

	s, _ := e.st.GetSession(context.Background(), "s1")
	assert.Equal(t, store.StatusApproved, s.Status)
	assert.False(t, s.MsgSynced)
}
