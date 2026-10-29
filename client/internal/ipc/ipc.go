// Package ipc is the protocol between the unprivileged app and the privileged daemon: one JSON line in, one out.
package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/ShotaKitazawa/MapleGate/client/internal/tunnel"
)

type Request struct {
	Op        string     `json:"op"` // up, down, status
	Up        *tunnel.Up `json:"up,omitempty"`
	SessionID string     `json:"session_id,omitempty"`
}

type Status struct {
	State     string    `json:"state"` // down, connecting, up
	Iface     string    `json:"iface,omitempty"`
	SessionID string    `json:"session_id,omitempty"`
	Since     time.Time `json:"since,omitempty"`
	// Error is why the last tunnel ended, if it ended badly; it stays until the next up.
	Error string `json:"error,omitempty"`
}

// Backend is what the daemon does; every call also counts as a sign that someone is looking after the tunnel.
type Backend interface {
	Up(Request) Status
	Down() Status
	Status() Status
}

type Server struct {
	Backend Backend
	// Authorize decides from the peer's uid whether to serve it.
	Authorize func(uid uint32) bool
	PeerUID   func(net.Conn) (uint32, error)
}

type reply struct {
	Status
	Err string `json:"err,omitempty"`
}

// Serve answers connections until ctx is done.
func (s *Server) Serve(ctx context.Context, l net.Listener) error {
	go func() { <-ctx.Done(); _ = l.Close() }()
	for {
		c, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go s.one(c)
	}
}

func (s *Server) one(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	enc := json.NewEncoder(c)
	uid, err := s.PeerUID(c)
	if err != nil || !s.Authorize(uid) {
		_ = enc.Encode(reply{Err: "not allowed"})
		return
	}
	var req Request
	line, err := bufio.NewReader(c).ReadBytes('\n')
	if err == nil {
		err = json.Unmarshal(line, &req)
	}
	if err != nil {
		_ = enc.Encode(reply{Err: "bad request"})
		return
	}
	switch req.Op {
	case "up":
		if req.Up == nil {
			_ = enc.Encode(reply{Err: "up needs the tunnel settings"})
			return
		}
		if err := req.Up.Validate(); err != nil {
			_ = enc.Encode(reply{Err: err.Error()})
			return
		}
		_ = enc.Encode(reply{Status: s.Backend.Up(req)})
	case "down":
		_ = enc.Encode(reply{Status: s.Backend.Down()})
	case "status":
		_ = enc.Encode(reply{Status: s.Backend.Status()})
	default:
		_ = enc.Encode(reply{Err: fmt.Sprintf("unknown op %q", req.Op)})
	}
}

type Client struct{ Path string }

func (c Client) Do(req Request) (Status, error) {
	conn, err := net.DialTimeout("unix", c.Path, 3*time.Second)
	if err != nil {
		return Status{}, fmt.Errorf("the VPN service is not running: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return Status{}, err
	}
	var r reply
	if err := json.NewDecoder(conn).Decode(&r); err != nil {
		return Status{}, err
	}
	if r.Err != "" {
		return Status{}, errors.New(r.Err)
	}
	return r.Status, nil
}
