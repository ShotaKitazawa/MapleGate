// maplegate signs in, asks for a session and holds the tunnel while it is open.
//
// Everything runs as the user except `maplegate tunnel`, which up starts through sudo and hands the key over on stdin.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/ShotaKitazawa/MapleGate/client/internal/auth"
	"github.com/ShotaKitazawa/MapleGate/client/internal/settings"
	"github.com/ShotaKitazawa/MapleGate/client/internal/tunnel"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sso"
	"github.com/aws/aws-sdk-go-v2/service/ssooidc"
)

// version is set at build time by build-pkg.sh.
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		return tray()
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

// newLogin reads the settings and prepares the sign in without signing in.
func newLogin(say func(string)) (settings.Settings, *auth.Login, error) {
	dir, err := settings.Dir()
	if err != nil {
		return settings.Settings{}, nil, err
	}
	s, err := settings.Load(dir)
	if err != nil {
		return s, nil, err
	}
	cfg := aws.Config{Region: s.Region, Credentials: aws.AnonymousCredentials{}}
	return s, &auth.Login{
		StartURL: s.StartURL, AccountID: s.AccountID, RoleName: s.RoleName, Dir: dir,
		OIDC: ssooidc.NewFromConfig(cfg), Portal: sso.NewFromConfig(cfg),
		Open: openURL,
		Say:  say,
	}, nil
}

func signIn(ctx context.Context) (settings.Settings, *auth.Login, error) {
	s, l, err := newLogin(func(m string) { fmt.Fprintln(os.Stderr, m) })
	if err != nil {
		return s, nil, err
	}
	if _, err := l.Provider().Retrieve(ctx); err != nil {
		return s, l, err
	}
	return s, l, nil
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
