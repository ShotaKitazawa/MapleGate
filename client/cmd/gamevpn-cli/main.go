// gamevpn-cli drives the session API end to end; the tray client replaces it in Phase 4.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/ShotaKitazawa/MapleGate/client/internal/api"
	"github.com/aws/aws-sdk-go-v2/config"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	url := flag.String("url", os.Getenv("GAMEVPN_API_URL"), "session API Function URL")
	wait := flag.Duration("wait", 10*time.Minute, "how long `up` waits for approval and the node")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: gamevpn-cli [-url URL] up | get <id> | delete <id>")
		flag.PrintDefaults()
	}
	flag.Parse()
	if *url == "" || flag.NArg() == 0 {
		flag.Usage()
		return errors.New("an API URL and a command are required")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return err
	}
	c := &api.Client{Base: *url, Region: cfg.Region, Creds: cfg.Credentials}

	switch cmd := flag.Arg(0); cmd {
	case "up":
		return up(ctx, c, *wait)
	case "get", "delete":
		if flag.NArg() != 2 {
			return fmt.Errorf("%s needs a session id", cmd)
		}
		var s api.Session
		if cmd == "get" {
			s, err = c.Get(ctx, flag.Arg(1))
		} else {
			s, err = c.Delete(ctx, flag.Arg(1))
		}
		if err != nil {
			return err
		}
		fmt.Printf("%s %s\n", s.ID, s.Status)
		return nil
	}
	return fmt.Errorf("unknown command %q", flag.Arg(0))
}

// up prints a wg-quick style config to stdout; progress goes to stderr so the config can be redirected.
func up(ctx context.Context, c *api.Client, wait time.Duration) error {
	key, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		return err
	}
	s, err := c.Create(ctx, key.PublicKey().String())
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "session %s: approve it in Discord\n", s.ID)

	deadline := time.Now().Add(wait)
	last := ""
	for {
		if s.Status != last {
			fmt.Fprintln(os.Stderr, "status:", s.Status)
			last = s.Status
		}
		if s.Terminal() {
			return fmt.Errorf("session %s ended: %s %s", s.ID, s.Status, s.Error)
		}
		if s.Status == "active" && s.Config != nil {
			fmt.Print(render(key, s.Config))
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("session %s is still %s; it stays open, `delete` it to give up", s.ID, s.Status)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
		next, err := c.Get(ctx, s.ID)
		if err != nil {
			// a failed poll says nothing about the session
			fmt.Fprintln(os.Stderr, "poll failed:", err)
			continue
		}
		s = next
	}
}

func render(key wgtypes.Key, c *api.Config) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[Interface]\nPrivateKey = %s\nAddress = %s\n\n", key, c.Address)
	fmt.Fprintf(&b, "[Peer]\nPublicKey = %s\nEndpoint = %s\nAllowedIPs = %s\n", c.ServerKey, c.Endpoint, strings.Join(c.AllowedIPs, ", "))
	if c.Keepalive > 0 {
		fmt.Fprintf(&b, "PersistentKeepalive = %d\n", c.Keepalive)
	}
	return b.String()
}
