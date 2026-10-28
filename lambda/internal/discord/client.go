package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const apiBase = "https://discord.com/api/v10"

type APIError struct {
	Method, Path string
	Status       int
	Body         string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("discord %s %s: %d %s", e.Method, e.Path, e.Status, e.Body)
}

type Client interface {
	PostMessage(ctx context.Context, channelID string, m Message) (messageID string, err error)
	UpdateMessage(ctx context.Context, channelID, messageID string, m Message) error
}

type RESTClient struct {
	// Token is resolved per request so a rotated bot token is picked up without a cold start.
	Token func(ctx context.Context) (string, error)
	// OnUnauthorized runs after a 401, giving the caller a chance to drop its cached token.
	OnUnauthorized func()
	HTTP           *http.Client
	Base           string
}

func NewREST(token func(ctx context.Context) (string, error), onUnauthorized func()) *RESTClient {
	return &RESTClient{Token: token, OnUnauthorized: onUnauthorized, HTTP: http.DefaultClient, Base: apiBase}
}

func (c *RESTClient) do(ctx context.Context, method, path string, in, out any) error {
	token, err := c.Token(ctx)
	if err != nil {
		return err
	}
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bot "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusUnauthorized && c.OnUnauthorized != nil {
		c.OnUnauthorized()
	}
	if resp.StatusCode/100 != 2 {
		return &APIError{Method: method, Path: path, Status: resp.StatusCode, Body: string(body)}
	}
	if out != nil {
		return json.Unmarshal(body, out)
	}
	return nil
}

func (c *RESTClient) PostMessage(ctx context.Context, channelID string, m Message) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, "/channels/"+channelID+"/messages", m, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

func (c *RESTClient) UpdateMessage(ctx context.Context, channelID, messageID string, m Message) error {
	return c.do(ctx, http.MethodPatch, "/channels/"+channelID+"/messages/"+messageID, m, nil)
}
