package sessionapi

import (
	"context"
	"encoding/base64"
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
	posted []discord.Message
	err    error
}

func (f *fakeDiscord) PostMessage(_ context.Context, _ string, m discord.Message) (string, error) {
	f.posted = append(f.posted, m)
	return "msg1", f.err
}
func (f *fakeDiscord) UpdateMessage(context.Context, string, string, discord.Message) error {
	return nil
}

// flaky fails every call once switched on, standing in for a DynamoDB outage on the Lambda side.
type flaky struct {
	*store.Mem
	down bool
}

func (f *flaky) GetSession(ctx context.Context, id string) (store.Session, error) {
	if f.down {
		return store.Session{}, errors.New("dynamodb unavailable")
	}
	return f.Mem.GetSession(ctx, id)
}

func (f *flaky) CreateSession(ctx context.Context, s store.Session, now time.Time) error {
	if f.down {
		return errors.New("dynamodb unavailable")
	}
	return f.Mem.CreateSession(ctx, s, now)
}

var pubKey = base64.StdEncoding.EncodeToString(make([]byte, 32))

type env struct {
	h   *Handler
	st  *flaky
	dc  *fakeDiscord
	now time.Time
}

func newEnv() *env {
	e := &env{st: &flaky{Mem: store.NewMem()}, dc: &fakeDiscord{}, now: time.Unix(1_000_000, 0)}
	n := 0
	e.h = &Handler{
		Cfg: config.Config{
			PendingTTL: 15 * time.Minute, StaleAfter: 2 * time.Minute, WGPort: 51820,
			AllowedIPs: []string{"0.0.0.0/0", "::/0"}, Keepalive: 25,
		},
		Store: e.st, Discord: e.dc,
		Auth:            IAMAuth{},
		ChannelID:       func(context.Context) (string, error) { return "ch", nil },
		ServerPublicKey: func(context.Context) (string, error) { return "SERVERKEY", nil },
		Now:             func() time.Time { return e.now },
		NewID:           func() string { n++; return "sid" + string(rune('0'+n)) },
	}
	return e
}

func req(method, path, user, body string) events.LambdaFunctionURLRequest {
	r := events.LambdaFunctionURLRequest{Body: body}
	r.RequestContext.Authorizer = SSOCaller(user)
	r.RequestContext.HTTP.Method = method
	r.RequestContext.HTTP.Path = path
	return r
}

func (e *env) do(method, path, user, body string) events.LambdaFunctionURLResponse {
	return e.h.Handle(context.Background(), req(method, path, user, body))
}

func body(t *testing.T, r events.LambdaFunctionURLResponse) map[string]any {
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(r.Body), &m))
	return m
}

func createBody() string { return `{"public_key":"` + pubKey + `"}` }

func TestAuthRequired(t *testing.T) {
	e := newEnv()
	for name, arn := range map[string]string{
		"an IAM user":                     "arn:aws:iam::123456789012:user/alice",
		"a role that is not SSO":          "arn:aws:sts::123456789012:assumed-role/ci/alice",
		"an SSO role without a user name": "arn:aws:sts::123456789012:assumed-role/AWSReservedSSO_Player_0123456789abcdef/",
		"nothing":                         "",
	} {
		r := req("POST", "/session", "alice", "{}")
		r.RequestContext.Authorizer.IAM.UserARN = arn
		assert.Equal(t, 403, e.h.Handle(context.Background(), r).StatusCode, name)
	}
	r := req("POST", "/session", "alice", "{}")
	r.RequestContext.Authorizer = nil
	assert.Equal(t, 403, e.h.Handle(context.Background(), r).StatusCode, "an unsigned request")
}

func TestUserIsTheIdentityCenterUserName(t *testing.T) {
	user, ok := IAMAuth{}.Authenticate(req("GET", "/session", "alice@example.com", ""))
	assert.True(t, ok)
	assert.Equal(t, "alice@example.com", user)
}

func TestCreateSession(t *testing.T) {
	e := newEnv()
	r := e.do("POST", "/session", "alice", createBody())

	assert.Equal(t, 202, r.StatusCode)
	assert.Equal(t, "pending", body(t, r)["status"])
	require.Len(t, e.dc.posted, 1)
	assert.Equal(t, "approve:sid1", e.dc.posted[0].Components[0].Components[0].CustomID)
	w, err := e.st.GetWork(context.Background(), "sid1")
	require.NoError(t, err)
	assert.Equal(t, "msg1", w.DiscordMessageID, "the janitor reads the message id from WORK")
}

func TestCreateSessionWithoutApproval(t *testing.T) {
	e := newEnv()
	e.h.Cfg.AutoApprove = true
	e.h.Cfg.WGSubnet = "10.66.0"
	e.h.Cfg.SessionTTL = 4 * time.Hour
	r := e.do("POST", "/session", "alice", createBody())

	assert.Equal(t, 202, r.StatusCode)
	assert.Equal(t, "approved", body(t, r)["status"])
	assert.Empty(t, e.dc.posted, "nothing is asked of Discord")
	s, err := e.st.GetSession(context.Background(), "sid1")
	require.NoError(t, err)
	assert.Equal(t, store.StatusApproved, s.Status)
	assert.NotEmpty(t, s.Address)
	assert.Equal(t, e.now.Add(4*time.Hour), s.ExpiresAt)
	w, err := e.st.GetWork(context.Background(), "sid1")
	require.NoError(t, err)
	assert.Empty(t, w.DiscordMessageID, "the janitor has no message to keep in step")
}

func TestCreateSessionValidation(t *testing.T) {
	e := newEnv()
	for _, b := range []string{`{}`, `{"public_key":"abc"}`, `not json`} {
		assert.Equal(t, 400, e.do("POST", "/session", "alice", b).StatusCode, b)
	}
}

func TestCreateSessionLimits(t *testing.T) {
	e := newEnv()
	assert.Equal(t, 202, e.do("POST", "/session", "alice", createBody()).StatusCode)

	r := e.do("POST", "/session", "alice", createBody())
	assert.Equal(t, 409, r.StatusCode)
	assert.Equal(t, "sid1", body(t, r)["session_id"], "a client that lost the first response can resume")

	_ = e.st.ClearActive(context.Background(), "alice", "sid1")
	assert.Equal(t, 202, e.do("POST", "/session", "alice", createBody()).StatusCode, "a freed slot allows a new request at once")
}

func TestCreateSessionDiscordFailureFreesSlot(t *testing.T) {
	e := newEnv()
	e.dc.err = errors.New("boom")
	assert.Equal(t, 502, e.do("POST", "/session", "alice", createBody()).StatusCode)

	s, _ := e.st.GetSession(context.Background(), "sid1")
	assert.Equal(t, store.StatusFailed, s.Status)

	e.dc.err = nil
	assert.Equal(t, 202, e.do("POST", "/session", "alice", createBody()).StatusCode, "our outage must not leave the slot taken")
}

func TestStoreOutageIs503(t *testing.T) {
	e := newEnv()
	e.st.down = true
	assert.Equal(t, 503, e.do("POST", "/session", "alice", createBody()).StatusCode)
	assert.Equal(t, 503, e.do("GET", "/session/sid1", "alice", "").StatusCode, "an outage must not look like a missing session")
	assert.Equal(t, 503, e.do("DELETE", "/session/sid1", "alice", "").StatusCode)
}

func approve(t *testing.T, e *env) {
	ctx := context.Background()
	require.NoError(t, e.st.CreateSession(ctx, store.Session{
		ID: "s1", User: "alice", PublicKey: pubKey, Status: store.StatusPending, ExpiresAt: e.now.Add(15 * time.Minute),
	}, e.now))
	require.NoError(t, e.st.Transition(ctx, "s1", []store.Status{store.StatusPending}, store.StatusApproved,
		store.Update{Address: "10.66.0.2", ExpiresAt: e.now.Add(4 * time.Hour), ApprovedAt: e.now}))
}

func TestGetApprovedIsProvisioningUntilNodeApplies(t *testing.T) {
	e := newEnv()
	approve(t, e)
	ctx := context.Background()

	r := e.do("GET", "/session/s1", "alice", "")
	assert.Equal(t, "provisioning", body(t, r)["status"])
	assert.Nil(t, body(t, r)["config"])

	require.NoError(t, e.st.PutNode(ctx, store.Node{InstanceID: "i-1", BootID: "b1", PublicIP: "1.2.3.4", LastSeen: e.now}, e.now, time.Minute))
	require.NoError(t, e.st.ReportApplied(ctx, "s1", "b1", e.now))

	r = e.do("GET", "/session/s1", "alice", "")
	assert.Equal(t, "active", body(t, r)["status"])
	cfg := body(t, r)["config"].(map[string]any)
	assert.Equal(t, "1.2.3.4:51820", cfg["endpoint"])
	assert.Equal(t, "10.66.0.2/32", cfg["address"])
	assert.Equal(t, "SERVERKEY", cfg["server_public_key"])
}

func TestGetFallsBackToProvisioningWhenNodeGoesQuiet(t *testing.T) {
	e := newEnv()
	approve(t, e)
	ctx := context.Background()
	require.NoError(t, e.st.PutNode(ctx, store.Node{InstanceID: "i-1", BootID: "b1", PublicIP: "1.2.3.4", LastSeen: e.now}, e.now, time.Minute))
	require.NoError(t, e.st.ReportApplied(ctx, "s1", "b1", e.now))

	e.now = e.now.Add(5 * time.Minute)
	r := e.do("GET", "/session/s1", "alice", "")
	assert.Equal(t, "provisioning", body(t, r)["status"])
}

func TestGetOtherUsersSessionIs404(t *testing.T) {
	e := newEnv()
	approve(t, e)
	assert.Equal(t, 404, e.do("GET", "/session/s1", "mallory", "").StatusCode)
	assert.Equal(t, 404, e.do("GET", "/session/nope", "alice", "").StatusCode)
}

func TestGetExpiresLazily(t *testing.T) {
	e := newEnv()
	approve(t, e)
	e.now = e.now.Add(5 * time.Hour)

	r := e.do("GET", "/session/s1", "alice", "")
	assert.Equal(t, "expired", body(t, r)["status"])
	assert.Nil(t, body(t, r)["config"])
	w, _ := e.st.GetWork(context.Background(), "s1")
	assert.Equal(t, store.StatusExpired, w.Phase, "the node learns about it through WORK")
}

func TestGetCurrentSession(t *testing.T) {
	e := newEnv()
	assert.Equal(t, 404, e.do("GET", "/session", "alice", "").StatusCode)
	approve(t, e)
	r := e.do("GET", "/session", "alice", "")
	assert.Equal(t, 200, r.StatusCode)
	assert.Equal(t, "s1", body(t, r)["session_id"])
}

func TestDeleteOnlyRecordsIntent(t *testing.T) {
	e := newEnv()
	approve(t, e)
	r := e.do("DELETE", "/session/s1", "alice", "")

	assert.Equal(t, 200, r.StatusCode)
	assert.Equal(t, "closed", body(t, r)["status"])
	w, _ := e.st.GetWork(context.Background(), "s1")
	assert.Equal(t, store.StatusClosed, w.Phase)
	assert.True(t, w.RemovedAt.IsZero(), "removal is the node's to confirm")

	assert.Equal(t, 200, e.do("DELETE", "/session/s1", "alice", "").StatusCode, "idempotent")
}

func TestDeletePendingAllowsANewRequest(t *testing.T) {
	e := newEnv()
	assert.Equal(t, 202, e.do("POST", "/session", "alice", createBody()).StatusCode)
	assert.Equal(t, 200, e.do("DELETE", "/session/sid1", "alice", "").StatusCode)
	assert.Equal(t, 202, e.do("POST", "/session", "alice", createBody()).StatusCode)
}
