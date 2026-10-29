//go:build !windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/ShotaKitazawa/MapleGate/client/internal/api"
	"github.com/ShotaKitazawa/MapleGate/client/internal/session"
	"github.com/ShotaKitazawa/MapleGate/client/internal/tunnel"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

// up asks for the root password first, so the person is not kept waiting at it after the approval.
func up(ctx context.Context) error {
	if err := interactive(exec.Command("sudo", "-v")).Run(); err != nil {
		return fmt.Errorf("sudo: %w", err)
	}
	s, l, err := signIn(ctx)
	if err != nil {
		return err
	}
	client := &api.Client{Base: s.APIURL, Region: s.Region, Creds: l.Provider()}
	r := &session.Runner{API: client, Progress: func(m string) { fmt.Fprintln(os.Stderr, m) }}

	key, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		return err
	}
	sess, err := r.Request(ctx, key.PublicKey().String(), 10*time.Minute)
	if err != nil {
		if sess.ID != "" && !sess.Terminal() {
			_ = r.Close(sess.ID)
		}
		return err
	}
	defer func() {
		if err := r.Close(sess.ID); err != nil {
			fmt.Fprintln(os.Stderr, "could not close the session:", err)
		}
	}()

	self, err := os.Executable()
	if err != nil {
		return err
	}
	child := exec.Command("sudo", self, "tunnel")
	child.Stdout, child.Stderr = os.Stderr, os.Stderr
	in, err := child.StdinPipe()
	if err != nil {
		return err
	}
	if err := child.Start(); err != nil {
		return err
	}
	c := sess.Config
	err = json.NewEncoder(in).Encode(tunnel.Up{
		PrivateKey: key.String(), Address: c.Address, ServerKey: c.ServerKey,
		Endpoint: c.Endpoint, AllowedIPs: c.AllowedIPs, Keepalive: c.Keepalive,
	})
	if err != nil {
		_ = in.Close()
		_ = child.Wait()
		return err
	}
	fmt.Fprintln(os.Stderr, "connected; press Ctrl-C to disconnect")

	exited := make(chan error, 1)
	go func() { exited <- child.Wait() }()
	ended := make(chan api.Session, 1)
	go func() {
		if e, err := r.Watch(ctx, sess.ID); err == nil {
			ended <- e
		}
	}()

	var result error
	select {
	case <-ctx.Done():
	case e := <-ended:
		result = fmt.Errorf("the session ended: %s %s", e.Status, e.Error)
	case err := <-exited:
		return fmt.Errorf("the tunnel stopped: %v", err)
	}
	// closing stdin is what tells the tunnel to remove its routes
	_ = in.Close()
	<-exited
	return result
}

func interactive(c *exec.Cmd) *exec.Cmd {
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c
}
