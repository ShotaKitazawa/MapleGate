package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ShotaKitazawa/MapleGate/internal/store"
	"github.com/ShotaKitazawa/MapleGate/node/agent/agent"
	"github.com/ShotaKitazawa/MapleGate/node/agent/wg"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

const healthFile = "/run/gamevpn/healthy"

func main() {
	if err := run(); err != nil {
		slog.Error("vpn-agent", "err", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	table := os.Getenv("TABLE_NAME")
	if table == "" {
		return errors.New("TABLE_NAME is required")
	}
	subnet, err := netip.ParsePrefix(envOr("WG_SUBNET", "10.66.0.0/24"))
	if err != nil {
		return err
	}
	awsCfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return err
	}
	meta := imds.NewFromConfig(awsCfg)
	instanceID, err := metadata(ctx, meta, "instance-id")
	if err != nil {
		return fmt.Errorf("instance id: %w", err)
	}
	publicIP, err := metadata(ctx, meta, "public-ipv4")
	if err != nil {
		return fmt.Errorf("public ip: %w", err)
	}
	bootID, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return err
	}

	a := &agent.Agent{
		Cfg: agent.Config{
			Subnet: subnet, MaxPeers: 16, CallTimeout: 5 * time.Second, BlindAfter: 3,
			BootGrace: envDur("BOOT_GRACE", 10*time.Minute), IdleStop: envDur("IDLE_STOP", 15*time.Minute),
			BlindPeer0Stop: envDur("BLIND_PEER0_STOP", 30*time.Minute), LocalHandshakeTTL: envDur("HANDSHAKE_IDLE", 10*time.Minute),
			Heartbeat: 30 * time.Second, HandshakeEvery: time.Minute, TakeoverAfter: 5 * time.Minute,
		},
		Store:      &store.Dynamo{Client: dynamodb.NewFromConfig(awsCfg), Table: table},
		WG:         wg.Ctrl{Device: envOr("WG_IF", "wg0")},
		Cache:      agent.FileCache{Path: envOr("CACHE_PATH", "/var/lib/gamevpn/state.json")},
		Now:        time.Now,
		Uptime:     uptime,
		Poweroff:   func() error { return exec.Command("systemctl", "poweroff").Run() },
		InstanceID: instanceID,
		BootID:     strings.TrimSpace(string(bootID)),
		PublicIP:   publicIP,
	}

	t := time.NewTicker(envDur("TICK", 10*time.Second))
	defer t.Stop()
	for {
		tctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		err := a.Tick(tctx)
		cancel()
		if err != nil {
			slog.Error("tick", "err", err)
		} else {
			notify("WATCHDOG=1")
			if a.Healthy() {
				markHealthy()
				notify("READY=1")
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

func metadata(ctx context.Context, c *imds.Client, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := c.GetMetadata(ctx, &imds.GetMetadataInput{Path: path})
	if err != nil {
		return "", err
	}
	defer out.Content.Close()
	var b strings.Builder
	buf := make([]byte, 256)
	for {
		n, err := out.Content.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return strings.TrimSpace(b.String()), nil
}

func uptime() time.Duration {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	f, err := strconv.ParseFloat(strings.Fields(string(b))[0], 64)
	if err != nil {
		return 0
	}
	return time.Duration(f * float64(time.Second))
}

func markHealthy() {
	_ = os.MkdirAll("/run/gamevpn", 0o755)
	_ = os.WriteFile(healthFile, nil, 0o644)
}

// notify speaks sd_notify directly; pulling a dependency in for two datagrams is not worth it.
func notify(state string) {
	sock := os.Getenv("NOTIFY_SOCKET")
	if sock == "" {
		return
	}
	c, err := net.Dial("unixgram", sock)
	if err != nil {
		return
	}
	defer c.Close()
	_, _ = c.Write([]byte(state))
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envDur(k string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(os.Getenv(k)); err == nil {
		return d
	}
	return def
}
