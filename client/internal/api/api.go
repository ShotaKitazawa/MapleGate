// Package api calls the session API, signing every request with SigV4 because the Function URL uses AWS_IAM.
package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

type Client struct {
	Base   string
	Region string
	Creds  aws.CredentialsProvider
	HTTP   *http.Client
	Now    func() time.Time
}

type Config struct {
	Endpoint   string   `json:"endpoint"`
	ServerKey  string   `json:"server_public_key"`
	Address    string   `json:"address"`
	AllowedIPs []string `json:"allowed_ips"`
	Keepalive  int      `json:"persistent_keepalive"`
	ExpiresAt  string   `json:"expires_at"`
}

type Session struct {
	ID     string  `json:"session_id"`
	Status string  `json:"status"`
	Error  string  `json:"error"`
	Config *Config `json:"config"`
	// Conflict is set when the server answered 409: this is an earlier session, not the one just asked for.
	Conflict bool `json:"-"`
}

// Terminal reports whether the session has ended; anything else, including an API error, must not drop a tunnel.
func (s Session) Terminal() bool {
	switch s.Status {
	case "denied", "failed", "closed", "expired":
		return true
	}
	return false
}

type StatusError struct {
	Code int
	Body string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("session API returned %d: %s", e.Code, e.Body)
}

func (c *Client) do(ctx context.Context, method, path string, body []byte) (Session, error) {
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.Base, "/")+path, bytes.NewReader(body))
	if err != nil {
		return Session{}, err
	}
	creds, err := c.Creds.Retrieve(ctx)
	if err != nil {
		return Session{}, fmt.Errorf("load AWS credentials (is the SSO session valid? try `aws sso login`): %w", err)
	}
	sum := sha256.Sum256(body)
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	if err := v4.NewSigner().SignHTTP(ctx, creds, req, hex.EncodeToString(sum[:]), "lambda", c.Region, now()); err != nil {
		return Session{}, err
	}
	h := c.HTTP
	if h == nil {
		h = http.DefaultClient
	}
	resp, err := h.Do(req)
	if err != nil {
		return Session{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return Session{}, err
	}
	var s Session
	if resp.StatusCode/100 != 2 && resp.StatusCode != http.StatusConflict {
		return Session{}, &StatusError{Code: resp.StatusCode, Body: strings.TrimSpace(string(raw))}
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return Session{}, fmt.Errorf("decode response: %w", err)
	}
	s.Conflict = resp.StatusCode == http.StatusConflict
	return s, nil
}

// Create returns the existing session on 409 with Conflict set; the caller cannot use it unless it holds that session's key.
func (c *Client) Create(ctx context.Context, publicKey string) (Session, error) {
	b, _ := json.Marshal(map[string]string{"public_key": publicKey})
	return c.do(ctx, http.MethodPost, "/session", b)
}

func (c *Client) Get(ctx context.Context, id string) (Session, error) {
	return c.do(ctx, http.MethodGet, "/session/"+id, nil)
}

func (c *Client) Delete(ctx context.Context, id string) (Session, error) {
	return c.do(ctx, http.MethodDelete, "/session/"+id, nil)
}
