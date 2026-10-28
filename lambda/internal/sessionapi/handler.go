package sessionapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/ShotaKitazawa/MapleGate/internal/store"
	"github.com/ShotaKitazawa/MapleGate/lambda/internal/config"
	"github.com/ShotaKitazawa/MapleGate/lambda/internal/discord"
	"github.com/ShotaKitazawa/MapleGate/lambda/internal/ec2ctl"
	"github.com/aws/aws-lambda-go/events"
)

type Handler struct {
	Cfg       config.Config
	Store     store.Store
	EC2       ec2ctl.Controller
	Discord   discord.Client
	Auth      Authenticator
	ChannelID func(ctx context.Context) (string, error)
	// ServerPublicKey is read from Parameter Store because the node writes it there at boot.
	ServerPublicKey func(ctx context.Context) (string, error)
	Now             func() time.Time
	NewID           func() string
}

func (h *Handler) Handle(ctx context.Context, req events.LambdaFunctionURLRequest) events.LambdaFunctionURLResponse {
	user, ok := h.Auth.Authenticate(req)
	if !ok {
		return jsonResp(http.StatusForbidden, map[string]string{"error": "forbidden"})
	}
	method, path := req.RequestContext.HTTP.Method, strings.TrimSuffix(req.RequestContext.HTTP.Path, "/")
	switch {
	case method == http.MethodPost && path == "/session":
		return h.create(ctx, user, req)
	case method == http.MethodGet && path == "/session":
		return h.current(ctx, user)
	case method == http.MethodGet && strings.HasPrefix(path, "/session/"):
		return h.get(ctx, user, strings.TrimPrefix(path, "/session/"))
	case method == http.MethodDelete && strings.HasPrefix(path, "/session/"):
		return h.del(ctx, user, strings.TrimPrefix(path, "/session/"))
	}
	return jsonResp(http.StatusNotFound, map[string]string{"error": "not found"})
}

func (h *Handler) create(ctx context.Context, user string, req events.LambdaFunctionURLRequest) events.LambdaFunctionURLResponse {
	body := req.Body
	if req.IsBase64Encoded {
		b, err := base64.StdEncoding.DecodeString(body)
		if err != nil {
			return jsonResp(http.StatusBadRequest, map[string]string{"error": "invalid body"})
		}
		body = string(b)
	}
	var in struct {
		PublicKey string `json:"public_key"`
	}
	if err := json.Unmarshal([]byte(body), &in); err != nil || !validPublicKey(in.PublicKey) {
		return jsonResp(http.StatusBadRequest, map[string]string{"error": "invalid public_key"})
	}

	now := h.Now()
	s := store.Session{
		ID: h.NewID(), User: user, PublicKey: in.PublicKey, Status: store.StatusPending,
		CreatedAt: now, ExpiresAt: now.Add(h.Cfg.PendingTTL),
	}
	switch err := h.Store.CreateSession(ctx, s, now); {
	case errors.Is(err, store.ErrActive):
		out := map[string]string{"error": "session already active"}
		if sid, err := h.Store.ActiveSessionID(ctx, user); err == nil {
			out["session_id"] = sid
		}
		return jsonResp(http.StatusConflict, out)
	case err != nil:
		slog.Error("create session", "err", err)
		return jsonResp(http.StatusServiceUnavailable, map[string]string{"error": "temporarily unavailable"})
	}

	// Starting is best effort: the janitor starts the node for any live session it finds stopped.
	if err := h.EC2.EnsureStarted(ctx); err != nil {
		slog.Warn("start instance", "session", s.ID, "err", err)
	}
	if err := h.postApproval(ctx, s); err != nil {
		slog.Error("post approval", "session", s.ID, "err", err)
		_ = h.Store.Transition(ctx, s.ID, []store.Status{store.StatusPending}, store.StatusFailed,
			store.Update{Error: "discord unavailable", CloseReason: "error"})
		return jsonResp(http.StatusBadGateway, map[string]string{"error": "approval request failed"})
	}
	return jsonResp(http.StatusAccepted, map[string]string{"session_id": s.ID, "status": string(store.StatusPending)})
}

func (h *Handler) postApproval(ctx context.Context, s store.Session) error {
	ch, err := h.ChannelID(ctx)
	if err != nil {
		return fmt.Errorf("channel id: %w", err)
	}
	msgID, err := h.Discord.PostMessage(ctx, ch, discord.ApprovalMessage(s.ID, s.User))
	if err != nil {
		return err
	}
	return h.Store.SetMessageID(ctx, s.ID, msgID)
}

// owned reports a missing or foreign session as not found, but an unreadable store as an error:
// a client must never mistake an outage for the session being gone.
func (h *Handler) owned(ctx context.Context, user, id string) (store.Session, bool, error) {
	s, err := h.Store.GetSession(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return store.Session{}, false, nil
	}
	if err != nil {
		return store.Session{}, false, err
	}
	return s, s.User == user, nil
}

func (h *Handler) current(ctx context.Context, user string) events.LambdaFunctionURLResponse {
	sid, err := h.Store.ActiveSessionID(ctx, user)
	if errors.Is(err, store.ErrNotFound) {
		return jsonResp(http.StatusNotFound, map[string]string{"error": "no session"})
	}
	if err != nil {
		return unavailable(err)
	}
	return h.get(ctx, user, sid)
}

func (h *Handler) get(ctx context.Context, user, id string) events.LambdaFunctionURLResponse {
	s, ok, err := h.owned(ctx, user, id)
	if err != nil {
		return unavailable(err)
	}
	if !ok {
		return jsonResp(http.StatusNotFound, map[string]string{"error": "not found"})
	}
	now := h.Now()
	// Evaluated here too so the API stays correct even when the janitor is not running.
	if s.Status.Live() && now.After(s.ExpiresAt) {
		if err := h.Store.Transition(ctx, s.ID, []store.Status{s.Status}, store.StatusExpired, store.Update{CloseReason: "expired"}); err == nil {
			s.Status = store.StatusExpired
		}
	}

	var w store.Work
	var n store.Node
	if s.Status == store.StatusApproved {
		if w, err = h.Store.GetWork(ctx, s.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
			return unavailable(err)
		}
		if n, err = h.Store.GetNode(ctx); err != nil && !errors.Is(err, store.ErrNotFound) {
			return unavailable(err)
		}
	}
	status := store.Visible(s.Status, w, n, now, h.Cfg.StaleAfter)

	out := map[string]any{"session_id": s.ID, "status": status}
	if s.Error != "" {
		out["error"] = s.Error
	}
	if status == "active" && n.PublicIP != "" {
		pub, err := h.ServerPublicKey(ctx)
		if err != nil {
			return unavailable(err)
		}
		out["config"] = map[string]any{
			"endpoint":             fmt.Sprintf("%s:%d", n.PublicIP, h.Cfg.WGPort),
			"server_public_key":    pub,
			"address":              s.Address + "/32",
			"allowed_ips":          h.Cfg.AllowedIPs,
			"persistent_keepalive": h.Cfg.Keepalive,
			"expires_at":           s.ExpiresAt.UTC().Format(time.RFC3339),
		}
	}
	return jsonResp(http.StatusOK, out)
}

// del only records the intent; the node removes the peer on its next tick and the janitor frees the IP afterwards.
func (h *Handler) del(ctx context.Context, user, id string) events.LambdaFunctionURLResponse {
	s, ok, err := h.owned(ctx, user, id)
	if err != nil {
		return unavailable(err)
	}
	if !ok {
		return jsonResp(http.StatusNotFound, map[string]string{"error": "not found"})
	}
	if !s.Status.Live() {
		return jsonResp(http.StatusOK, map[string]any{"status": s.Status})
	}
	err = h.Store.Transition(ctx, s.ID, []store.Status{s.Status}, store.StatusClosed,
		store.Update{CloseReason: "user"})
	if errors.Is(err, store.ErrConflict) {
		return jsonResp(http.StatusConflict, map[string]string{"error": "state changed, retry"})
	}
	if err != nil {
		return unavailable(err)
	}
	return jsonResp(http.StatusOK, map[string]any{"status": store.StatusClosed})
}

func unavailable(err error) events.LambdaFunctionURLResponse {
	slog.Error("store", "err", err)
	return jsonResp(http.StatusServiceUnavailable, map[string]string{"error": "temporarily unavailable"})
}

func validPublicKey(k string) bool {
	b, err := base64.StdEncoding.DecodeString(k)
	return err == nil && len(b) == 32
}

func jsonResp(code int, v any) events.LambdaFunctionURLResponse {
	b, _ := json.Marshal(v)
	return events.LambdaFunctionURLResponse{
		StatusCode: code,
		Headers:    map[string]string{"content-type": "application/json"},
		Body:       string(b),
	}
}
