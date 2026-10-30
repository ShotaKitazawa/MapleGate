package auth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sso"
	ssotypes "github.com/aws/aws-sdk-go-v2/service/sso/types"
	"github.com/aws/aws-sdk-go-v2/service/ssooidc"
	oidctypes "github.com/aws/aws-sdk-go-v2/service/ssooidc/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeOIDC struct {
	registers, starts int
	pendingFor        int
	slowOnce          bool
	refreshErr        error
	grants            []string
	tokens            int
}

func (f *fakeOIDC) RegisterClient(context.Context, *ssooidc.RegisterClientInput, ...func(*ssooidc.Options)) (*ssooidc.RegisterClientOutput, error) {
	f.registers++
	return &ssooidc.RegisterClientOutput{ClientId: aws.String("cid"), ClientSecret: aws.String("sec"),
		ClientSecretExpiresAt: time.Now().Add(90 * 24 * time.Hour).Unix()}, nil
}

func (f *fakeOIDC) StartDeviceAuthorization(context.Context, *ssooidc.StartDeviceAuthorizationInput, ...func(*ssooidc.Options)) (*ssooidc.StartDeviceAuthorizationOutput, error) {
	f.starts++
	return &ssooidc.StartDeviceAuthorizationOutput{DeviceCode: aws.String("dev"), UserCode: aws.String("ABCD"),
		VerificationUriComplete: aws.String("https://example.invalid/verify"), ExpiresIn: 600, Interval: 1}, nil
}

func (f *fakeOIDC) CreateToken(_ context.Context, in *ssooidc.CreateTokenInput, _ ...func(*ssooidc.Options)) (*ssooidc.CreateTokenOutput, error) {
	f.grants = append(f.grants, aws.ToString(in.GrantType))
	if aws.ToString(in.GrantType) == "refresh_token" && f.refreshErr != nil {
		return nil, f.refreshErr
	}
	if aws.ToString(in.GrantType) != "refresh_token" {
		if f.slowOnce {
			f.slowOnce = false
			return nil, &oidctypes.SlowDownException{}
		}
		if f.pendingFor > 0 {
			f.pendingFor--
			return nil, &oidctypes.AuthorizationPendingException{}
		}
	}
	f.tokens++
	return &ssooidc.CreateTokenOutput{AccessToken: aws.String("access"), RefreshToken: aws.String("refresh"), ExpiresIn: 3600}, nil
}

type fakePortal struct {
	unauthorizedOnce bool
	calls            int
	sawToken         string
}

func (f *fakePortal) GetRoleCredentials(_ context.Context, in *sso.GetRoleCredentialsInput, _ ...func(*sso.Options)) (*sso.GetRoleCredentialsOutput, error) {
	f.calls++
	f.sawToken = aws.ToString(in.AccessToken)
	if f.unauthorizedOnce {
		f.unauthorizedOnce = false
		return nil, &ssotypes.UnauthorizedException{}
	}
	return &sso.GetRoleCredentialsOutput{RoleCredentials: &ssotypes.RoleCredentials{
		AccessKeyId: aws.String("AKIA"), SecretAccessKey: aws.String("s"), SessionToken: aws.String("t"),
		Expiration: time.Now().Add(time.Hour).UnixMilli()}}, nil
}

type rig struct {
	l      *Login
	oidc   *fakeOIDC
	portal *fakePortal
	opened []string
	slept  []time.Duration
	now    time.Time
}

func newRig(t *testing.T) *rig {
	r := &rig{oidc: &fakeOIDC{}, portal: &fakePortal{}, now: time.Now()}
	r.l = &Login{
		StartURL: "https://start", AccountID: "1", RoleName: "gamevpn-player", Dir: filepath.Join(t.TempDir(), "cache"),
		OIDC: r.oidc, Portal: r.portal,
		Open: func(u string) error { r.opened = append(r.opened, u); return nil },
		Now:  func() time.Time { return r.now },
		Sleep: func(_ context.Context, d time.Duration) error {
			r.slept = append(r.slept, d)
			r.now = r.now.Add(d)
			return nil
		},
	}
	return r
}

func TestFirstSignInOpensTheBrowserOnceAndWaitsThroughPending(t *testing.T) {
	r := newRig(t)
	r.oidc.pendingFor = 2

	tok, err := r.l.Token(context.Background())

	require.NoError(t, err)
	assert.Equal(t, "access", tok)
	assert.Equal(t, []string{"https://example.invalid/verify"}, r.opened)
	assert.Len(t, r.slept, 3)
}

func TestACachedTokenNeedsNoBrowserAndNoNetwork(t *testing.T) {
	r := newRig(t)
	_, err := r.l.Token(context.Background())
	require.NoError(t, err)
	r.opened, r.oidc.grants = nil, nil

	tok, err := r.l.Token(context.Background())

	require.NoError(t, err)
	assert.Equal(t, "access", tok)
	assert.Empty(t, r.opened)
	assert.Empty(t, r.oidc.grants)
}

func TestAnExpiredTokenIsRefreshedWithoutTheBrowser(t *testing.T) {
	r := newRig(t)
	_, err := r.l.Token(context.Background())
	require.NoError(t, err)
	r.opened, r.oidc.grants = nil, nil
	r.now = r.now.Add(2 * time.Hour)

	tok, err := r.l.Token(context.Background())

	require.NoError(t, err)
	assert.Equal(t, "access", tok)
	assert.Equal(t, []string{"refresh_token"}, r.oidc.grants)
	assert.Empty(t, r.opened)
}

func TestARefusedRefreshTokenFallsBackToTheDeviceFlow(t *testing.T) {
	r := newRig(t)
	_, err := r.l.Token(context.Background())
	require.NoError(t, err)
	r.opened = nil
	r.now = r.now.Add(2 * time.Hour)
	r.oidc.refreshErr = errors.New("invalid_grant")

	_, err = r.l.Token(context.Background())

	require.NoError(t, err)
	assert.Len(t, r.opened, 1)
}

func TestSlowDownLengthensThePollingInterval(t *testing.T) {
	r := newRig(t)
	r.oidc.slowOnce = true

	_, err := r.l.Token(context.Background())

	require.NoError(t, err)
	assert.Equal(t, []time.Duration{time.Second, 6 * time.Second}, r.slept)
}

func TestASignInThatIsNeverCompletedEnds(t *testing.T) {
	r := newRig(t)
	r.oidc.pendingFor = 1 << 30

	_, err := r.l.Token(context.Background())

	assert.Error(t, err)
}

func TestTheCacheIsPrivateToTheUser(t *testing.T) {
	r := newRig(t)
	_, err := r.l.Token(context.Background())
	require.NoError(t, err)

	d, err := os.Stat(r.l.Dir)
	require.NoError(t, err)
	f, err := os.Stat(filepath.Join(r.l.Dir, "sso.json"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), d.Mode().Perm())
	assert.Equal(t, os.FileMode(0o600), f.Mode().Perm())
}

func TestARoleCredentialRequestRetriesOnceAfterAnUnauthorizedToken(t *testing.T) {
	r := newRig(t)
	r.portal.unauthorizedOnce = true

	creds, err := r.l.Provider().Retrieve(context.Background())

	require.NoError(t, err)
	assert.Equal(t, "AKIA", creds.AccessKeyID)
	assert.Equal(t, 2, r.portal.calls)
	assert.Len(t, r.opened, 2)
}

func TestRoleCredentialsExpireBeforeAWSDoes(t *testing.T) {
	r := newRig(t)

	creds, err := r.l.Provider().Retrieve(context.Background())

	require.NoError(t, err)
	assert.True(t, creds.CanExpire)
	assert.True(t, creds.Expires.Before(time.Now().Add(time.Hour)))
}

func TestSignedInFollowsTheCachedTokens(t *testing.T) {
	r := newRig(t)
	assert.False(t, r.l.SignedIn())

	_, err := r.l.Token(context.Background())
	require.NoError(t, err)
	assert.True(t, r.l.SignedIn())

	r.l.Forget()
	assert.False(t, r.l.SignedIn())
}

func TestAPromptGetsTheAddressAndCodeInsteadOfASentence(t *testing.T) {
	r := newRig(t)
	var url, code string
	r.l.Prompt = func(u, c string) { url, code = u, c }
	var said []string
	r.l.Say = func(m string) { said = append(said, m) }

	_, err := r.l.Token(context.Background())

	require.NoError(t, err)
	assert.Equal(t, "https://example.invalid/verify", url)
	assert.Equal(t, "ABCD", code)
	assert.Empty(t, said)
}

func TestACancelledSignInDoesNotKeepRunningForTheNextRequest(t *testing.T) {
	r := newRig(t)
	entered := make(chan struct{}, 2)
	r.l.Sleep = func(ctx context.Context, _ time.Duration) error {
		entered <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}
	p := r.l.Provider()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := p.Retrieve(ctx); done <- err }()
	<-entered
	cancel()
	require.Error(t, <-done)

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	go func() { _, _ = p.Retrieve(ctx2) }()
	<-entered

	assert.Len(t, r.opened, 2)
}
