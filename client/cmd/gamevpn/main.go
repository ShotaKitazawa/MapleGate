// gamevpn signs in, asks for a session and holds the tunnel while it is open.
//
// Everything runs as the user except `gamevpn tunnel`, which up starts through sudo and hands the key over on stdin.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/ShotaKitazawa/MapleGate/client/internal/api"
	"github.com/ShotaKitazawa/MapleGate/client/internal/auth"
	"github.com/ShotaKitazawa/MapleGate/client/internal/session"
	"github.com/ShotaKitazawa/MapleGate/client/internal/settings"
	"github.com/ShotaKitazawa/MapleGate/client/internal/tunnel"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sso"
	"github.com/aws/aws-sdk-go-v2/service/ssooidc"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		return errors.New("usage: gamevpn login | up")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	switch os.Args[1] {
	case "login":
		_, _, err := signIn(ctx)
		return err
	case "up":
		return up(ctx)
	case "tunnel":
		return runTunnel(ctx, os.Stdin)
	}
	return fmt.Errorf("unknown command %q", os.Args[1])
}

func signIn(ctx context.Context) (settings.Settings, *auth.Login, error) {
	dir, err := settings.Dir()
	if err != nil {
		return settings.Settings{}, nil, err
	}
	s, err := settings.Load(dir)
	if err != nil {
		return s, nil, err
	}
	cfg := aws.Config{Region: s.Region, Credentials: aws.AnonymousCredentials{}}
	l := &auth.Login{
		StartURL: s.StartURL, AccountID: s.AccountID, RoleName: s.RoleName, Dir: dir,
		OIDC: ssooidc.NewFromConfig(cfg), Portal: sso.NewFromConfig(cfg),
		Open: func(u string) error { return exec.Command("open", u).Run() },
		Say:  func(m string) { fmt.Fprintln(os.Stderr, m) },
	}
	if _, err := l.Provider().Retrieve(ctx); err != nil {
		return s, l, err
	}
	return s, l, nil
}

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

// runTunnel is the privileged half. It ends when its stdin closes, so it cannot outlive the process that started it.
func runTunnel(ctx context.Context, stdin io.Reader) error {
	dec := json.NewDecoder(stdin)
	var u tunnel.Up
	if err := dec.Decode(&u); err != nil {
		return fmt.Errorf("read the tunnel settings: %w", err)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		_, _ = io.Copy(io.Discard, io.MultiReader(dec.Buffered(), stdin))
		cancel()
	}()
	return tunnel.Run(ctx, u, func(f string, a ...any) { fmt.Fprintf(os.Stderr, f+"\n", a...) }, nil)
}
