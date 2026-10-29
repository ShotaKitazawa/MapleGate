// Package auth signs a friend in through the Identity Center device flow, so no AWS CLI is needed on their machine.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sso"
	ssotypes "github.com/aws/aws-sdk-go-v2/service/sso/types"
	"github.com/aws/aws-sdk-go-v2/service/ssooidc"
	oidctypes "github.com/aws/aws-sdk-go-v2/service/ssooidc/types"
)

type OIDC interface {
	RegisterClient(context.Context, *ssooidc.RegisterClientInput, ...func(*ssooidc.Options)) (*ssooidc.RegisterClientOutput, error)
	StartDeviceAuthorization(context.Context, *ssooidc.StartDeviceAuthorizationInput, ...func(*ssooidc.Options)) (*ssooidc.StartDeviceAuthorizationOutput, error)
	CreateToken(context.Context, *ssooidc.CreateTokenInput, ...func(*ssooidc.Options)) (*ssooidc.CreateTokenOutput, error)
}

type Portal interface {
	GetRoleCredentials(context.Context, *sso.GetRoleCredentialsInput, ...func(*sso.Options)) (*sso.GetRoleCredentialsOutput, error)
}

type Login struct {
	StartURL  string
	AccountID string
	RoleName  string
	// Dir holds the cache; it is created 0700 and the files 0600.
	Dir    string
	OIDC   OIDC
	Portal Portal
	// Open shows the verification page to the person; its failure is not fatal because Say also prints the URL.
	Open func(url string) error
	Say  func(msg string)
	// Prompt receives the address and the code instead of Say, for a caller that shows them its own way.
	Prompt func(url, code string)
	Now    func() time.Time
	// Sleep waits between polls; tests replace it.
	Sleep func(context.Context, time.Duration) error
}

type cache struct {
	ClientID     string    `json:"client_id,omitempty"`
	ClientSecret string    `json:"client_secret,omitempty"`
	ClientExpiry time.Time `json:"client_expiry,omitempty"`
	AccessToken  string    `json:"access_token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenExpiry  time.Time `json:"token_expiry,omitempty"`
}

// refreshMargin keeps a token that would die mid-request from being used.
const refreshMargin = time.Minute

func (l *Login) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func (l *Login) say(m string) {
	if l.Say != nil {
		l.Say(m)
	}
}

func (l *Login) load() cache {
	var c cache
	if b, err := os.ReadFile(filepath.Join(l.Dir, "sso.json")); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	return c
}

func (l *Login) save(c cache) error {
	if err := os.MkdirAll(l.Dir, 0o700); err != nil {
		return err
	}
	b, _ := json.Marshal(c)
	tmp := filepath.Join(l.Dir, "sso.json.tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(l.Dir, "sso.json"))
}

// Token returns a valid access token: the cached one, a refreshed one, or one from a new device flow, in that order.
func (l *Login) Token(ctx context.Context) (string, error) {
	c := l.load()
	if c.AccessToken != "" && l.now().Add(refreshMargin).Before(c.TokenExpiry) {
		return c.AccessToken, nil
	}
	if c.RefreshToken != "" && c.ClientID != "" {
		if t, err := l.refresh(ctx, c); err == nil {
			return t, nil
		}
		// a refresh token that is refused is just gone; fall through to a full sign in
	}
	return l.deviceFlow(ctx, c)
}

func (l *Login) refresh(ctx context.Context, c cache) (string, error) {
	out, err := l.OIDC.CreateToken(ctx, &ssooidc.CreateTokenInput{
		ClientId: &c.ClientID, ClientSecret: &c.ClientSecret,
		GrantType: aws.String("refresh_token"), RefreshToken: &c.RefreshToken,
	})
	if err != nil {
		return "", err
	}
	return l.keep(c, out)
}

func (l *Login) keep(c cache, out *ssooidc.CreateTokenOutput) (string, error) {
	c.AccessToken = aws.ToString(out.AccessToken)
	c.TokenExpiry = l.now().Add(time.Duration(out.ExpiresIn) * time.Second)
	if r := aws.ToString(out.RefreshToken); r != "" {
		c.RefreshToken = r
	}
	if err := l.save(c); err != nil {
		return "", err
	}
	return c.AccessToken, nil
}

func (l *Login) register(ctx context.Context, c cache) (cache, error) {
	if c.ClientID != "" && l.now().Add(24*time.Hour).Before(c.ClientExpiry) {
		return c, nil
	}
	out, err := l.OIDC.RegisterClient(ctx, &ssooidc.RegisterClientInput{
		ClientName: aws.String("MapleGate"), ClientType: aws.String("public"), Scopes: []string{"sso:account:access"},
	})
	if err != nil {
		return c, fmt.Errorf("register client: %w", err)
	}
	c.ClientID, c.ClientSecret = aws.ToString(out.ClientId), aws.ToString(out.ClientSecret)
	c.ClientExpiry = time.Unix(out.ClientSecretExpiresAt, 0)
	c.AccessToken, c.RefreshToken = "", ""
	return c, nil
}

func (l *Login) deviceFlow(ctx context.Context, c cache) (string, error) {
	c, err := l.register(ctx, c)
	if err != nil {
		return "", err
	}
	auth, err := l.OIDC.StartDeviceAuthorization(ctx, &ssooidc.StartDeviceAuthorizationInput{
		ClientId: &c.ClientID, ClientSecret: &c.ClientSecret, StartUrl: &l.StartURL,
	})
	if err != nil {
		return "", fmt.Errorf("start device authorization: %w", err)
	}
	url := aws.ToString(auth.VerificationUriComplete)
	if l.Prompt != nil {
		l.Prompt(url, aws.ToString(auth.UserCode))
	} else {
		l.say(fmt.Sprintf("Sign in at %s (code %s)", url, aws.ToString(auth.UserCode)))
	}
	if l.Open != nil {
		_ = l.Open(url)
	}
	interval := time.Duration(auth.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	deadline := l.now().Add(time.Duration(auth.ExpiresIn) * time.Second)
	for l.now().Before(deadline) {
		if err := l.sleep(ctx, interval); err != nil {
			return "", err
		}
		out, err := l.OIDC.CreateToken(ctx, &ssooidc.CreateTokenInput{
			ClientId: &c.ClientID, ClientSecret: &c.ClientSecret,
			GrantType:  aws.String("urn:ietf:params:oauth:grant-type:device_code"),
			DeviceCode: auth.DeviceCode,
		})
		var pending *oidctypes.AuthorizationPendingException
		var slow *oidctypes.SlowDownException
		switch {
		case err == nil:
			return l.keep(c, out)
		case errors.As(err, &pending):
		case errors.As(err, &slow):
			interval += 5 * time.Second
		default:
			return "", fmt.Errorf("create token: %w", err)
		}
	}
	return "", errors.New("the sign in was not completed in time")
}

func (l *Login) sleep(ctx context.Context, d time.Duration) error {
	if l.Sleep != nil {
		return l.Sleep(ctx, d)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// SignedIn says whether tokens are cached; the refresh token may still be refused, which only a request finds out.
func (l *Login) SignedIn() bool {
	c := l.load()
	return c.RefreshToken != "" || (c.AccessToken != "" && l.now().Before(c.TokenExpiry))
}

// Forget drops the cached tokens, for when the portal says the token is no good.
func (l *Login) Forget() {
	c := l.load()
	c.AccessToken, c.RefreshToken = "", ""
	_ = l.save(c)
}

// Provider returns role credentials for the API client; the cache wrapper renews them before they expire.
func (l *Login) Provider() aws.CredentialsProvider {
	return aws.NewCredentialsCache(aws.CredentialsProviderFunc(l.retrieve))
}

func (l *Login) retrieve(ctx context.Context) (aws.Credentials, error) {
	for attempt := 0; ; attempt++ {
		tok, err := l.Token(ctx)
		if err != nil {
			return aws.Credentials{}, err
		}
		out, err := l.Portal.GetRoleCredentials(ctx, &sso.GetRoleCredentialsInput{
			AccessToken: &tok, AccountId: &l.AccountID, RoleName: &l.RoleName,
		})
		var unauth *ssotypes.UnauthorizedException
		if errors.As(err, &unauth) && attempt == 0 {
			l.Forget()
			continue
		}
		if err != nil {
			return aws.Credentials{}, fmt.Errorf("get role credentials: %w", err)
		}
		rc := out.RoleCredentials
		return aws.Credentials{
			AccessKeyID: aws.ToString(rc.AccessKeyId), SecretAccessKey: aws.ToString(rc.SecretAccessKey),
			SessionToken: aws.ToString(rc.SessionToken), CanExpire: true,
			Expires: time.UnixMilli(rc.Expiration).Add(-refreshMargin), Source: "maplegate-sso",
		}, nil
	}
}
