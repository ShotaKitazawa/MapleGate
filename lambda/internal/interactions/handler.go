package interactions

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ShotaKitazawa/MapleGate/internal/store"
	"github.com/ShotaKitazawa/MapleGate/lambda/internal/config"
	"github.com/ShotaKitazawa/MapleGate/lambda/internal/discord"
	"github.com/ShotaKitazawa/MapleGate/lambda/internal/ipam"
	"github.com/aws/aws-lambda-go/events"
)

type Invoker interface {
	// InvokeAsync runs this function again with payload without waiting (InvocationType=Event).
	InvokeAsync(ctx context.Context, payload []byte) error
}

type Approver struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type AsyncEvent struct {
	Action    string   `json:"action"`
	SessionID string   `json:"session_id"`
	Approver  Approver `json:"approver"`
}

type Handler struct {
	Cfg       config.Config
	Store     store.Store
	Discord   discord.Client
	Invoker   Invoker
	ChannelID func(ctx context.Context) (string, error)
	PublicKey func(ctx context.Context) (ed25519.PublicKey, error)
	// DownloadURL returns a short-lived link to the installer for a platform, or "" when there is none yet.
	DownloadURL func(ctx context.Context, platform string) (string, error)
	Now         func() time.Time
}

func (h *Handler) Handle(ctx context.Context, req events.LambdaFunctionURLRequest) events.LambdaFunctionURLResponse {
	body := []byte(req.Body)
	if req.IsBase64Encoded {
		b, err := base64.StdEncoding.DecodeString(req.Body)
		if err != nil {
			return resp(http.StatusBadRequest, nil)
		}
		body = b
	}
	pub, err := h.PublicKey(ctx)
	if err != nil {
		slog.Error("load app public key", "err", err)
		return resp(http.StatusInternalServerError, nil)
	}
	if !freshTimestamp(req.Headers["x-signature-timestamp"], h.Now()) || !discord.Verify(pub, req.Headers["x-signature-ed25519"], req.Headers["x-signature-timestamp"], body) {
		return resp(http.StatusUnauthorized, nil)
	}

	var in discord.Interaction
	if err := json.Unmarshal(body, &in); err != nil {
		return resp(http.StatusBadRequest, nil)
	}
	switch in.Type {
	case discord.InteractionPing:
		return resp(http.StatusOK, map[string]int{"type": discord.ResponsePong})
	case discord.InteractionCommand:
		platform, ok := strings.CutPrefix(in.Data.Name, "download-")
		if !ok || (platform != "macos" && platform != "windows") {
			return resp(http.StatusBadRequest, nil)
		}
		return h.download(ctx, platform)
	case discord.InteractionComponent:
		action, sid, ok := strings.Cut(in.Data.CustomID, ":")
		if !ok || (action != "approve" && action != "deny") || sid == "" {
			return resp(http.StatusBadRequest, nil)
		}
		payload, _ := json.Marshal(AsyncEvent{
			Action: action, SessionID: sid,
			Approver: Approver{ID: in.Member.User.ID, Name: in.Member.User.DisplayName()},
		})
		if err := h.Invoker.InvokeAsync(ctx, payload); err != nil {
			slog.Error("async invoke", "err", err)
			return resp(http.StatusInternalServerError, nil)
		}
		return resp(http.StatusOK, map[string]int{"type": discord.ResponseDeferredUpdateMessage})
	}
	return resp(http.StatusBadRequest, nil)
}

const maxContent = 2000

// hints are the places people get stuck, not the steps: the installer is unsigned and the first sign in needs a password set.
var hints = map[string]string{
	"macos": "- Not signed by Apple: right-click the file, Open, Open. If there is no such button: System Settings, Privacy & Security, Open Anyway.\n" +
		"- It asks for your Mac password; the menu bar then shows a dot (MapleGate is not under System Settings, VPN).\n" +
		"- Set your password from the AWS invitation e-mail first.\n" +
		"- Always click Disconnect when you are done.",
	"windows": "- \"Windows protected your PC\": More info, Run anyway. Allow the administrator prompt.\n" +
		"- The dot may be under the ^ arrow in the tray.\n" +
		"- Set your password from the AWS invitation e-mail first.\n" +
		"- Always click Disconnect when you are done.",
}

func (h *Handler) download(ctx context.Context, platform string) events.LambdaFunctionURLResponse {
	content := "The link could not be made. Ask the person who runs the server."
	if h.DownloadURL != nil {
		switch u, err := h.DownloadURL(ctx, platform); {
		case err != nil:
			slog.Error("download link", "err", err)
		case u == "":
			content = "There is no installer for " + platform + " yet."
		default:
			content = "[Download link](" + u + ") (works for 15 minutes)"
			// the signed URL carries a session token and can be long; a message over Discord's limit is refused whole
			if len(content)+len(hints[platform])+1 <= maxContent {
				content += "\n" + hints[platform]
			}
		}
	}
	return resp(http.StatusOK, map[string]any{
		"type": discord.ResponseChannelMessage,
		"data": map[string]any{"content": content, "flags": discord.FlagEphemeral},
	})
}

func (h *Handler) HandleAsync(ctx context.Context, ev AsyncEvent) error {
	s, err := h.Store.GetSession(ctx, ev.SessionID)
	if err != nil {
		return err
	}
	switch ev.Action {
	case "deny":
		if err := h.Store.Transition(ctx, s.ID, []store.Status{store.StatusPending}, store.StatusDenied,
			store.Update{Approver: ev.Approver.Name, CloseReason: "denied"}); err != nil {
			return h.lost(ctx, s, err)
		}
		h.updateMessage(ctx, s, "denied", ev.Approver.Name)
		return nil
	case "approve":
		return h.approve(ctx, s, ev.Approver)
	}
	return fmt.Errorf("unknown action %q", ev.Action)
}

// lost handles a transition that did not apply: a conflict means another press or the janitor won,
// and the message is refreshed so it never keeps offering buttons for a decided session.
func (h *Handler) lost(ctx context.Context, s store.Session, err error) error {
	if !errors.Is(err, store.ErrConflict) {
		return err
	}
	cur, gerr := h.Store.GetSession(ctx, s.ID)
	if gerr == nil && cur.Status != store.StatusPending {
		h.updateMessage(ctx, cur, outcome(cur.Status), cur.Approver)
	}
	return nil
}

func (h *Handler) approve(ctx context.Context, s store.Session, who Approver) error {
	now := h.Now()
	if now.After(s.ExpiresAt) {
		if err := h.Store.Transition(ctx, s.ID, []store.Status{store.StatusPending}, store.StatusExpired,
			store.Update{CloseReason: "approval timeout"}); err != nil {
			return h.lost(ctx, s, err)
		}
		h.updateMessage(ctx, s, "expired", who.Name)
		return nil
	}

	expires := now.Add(h.Cfg.SessionTTL)
	addr, err := ipam.Allocate(ctx, h.Store, h.Cfg.WGSubnet, s.ID, expires)
	if err != nil {
		slog.Error("allocate address", "session", s.ID, "err", err)
		if errors.Is(err, ipam.ErrExhausted) {
			_ = h.Store.Transition(ctx, s.ID, []store.Status{store.StatusPending}, store.StatusFailed,
				store.Update{Error: "no free address", CloseReason: "error", Approver: who.Name})
			h.updateMessage(ctx, s, "failed", who.Name)
			return nil
		}
		return err
	}
	// The pending->approved condition is what rejects a second press.
	err = h.Store.Transition(ctx, s.ID, []store.Status{store.StatusPending}, store.StatusApproved,
		store.Update{Address: addr, Approver: who.Name, ExpiresAt: expires, ApprovedAt: now})
	if err != nil {
		_ = h.Store.ReleaseIP(ctx, addr, s.ID)
		return h.lost(ctx, s, err)
	}
	h.updateMessage(ctx, s, "approved", who.Name)
	return nil
}

func outcome(st store.Status) string { return string(st) }

// updateMessage uses the bot token rather than the interaction token, which dies after 15 minutes.
// A failure leaves msg_synced false so the janitor retries.
func (h *Handler) updateMessage(ctx context.Context, s store.Session, outcome, approver string) {
	ch, err := h.ChannelID(ctx)
	if err != nil || s.DiscordMessageID == "" {
		slog.Error("update message: no channel/message", "session", s.ID, "err", err)
		return
	}
	m := discord.ResultMessage(s.ID, s.User, outcome, approver, h.Now().UTC().Format(time.RFC3339))
	if err := h.Discord.UpdateMessage(ctx, ch, s.DiscordMessageID, m); err != nil {
		slog.Error("update message", "session", s.ID, "err", err)
		return
	}
	_ = h.Store.SetMsgSynced(ctx, s.ID, true)
}

func resp(code int, v any) events.LambdaFunctionURLResponse {
	r := events.LambdaFunctionURLResponse{StatusCode: code, Headers: map[string]string{"content-type": "application/json"}}
	if v != nil {
		b, _ := json.Marshal(v)
		r.Body = string(b)
	}
	return r
}

// Discord signs the timestamp, so bounding it limits how long a captured request stays replayable.
func freshTimestamp(ts string, now time.Time) bool {
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false
	}
	d := now.Sub(time.Unix(sec, 0))
	return d < tsTolerance && d > -tsTolerance
}

const tsTolerance = 5 * time.Minute
