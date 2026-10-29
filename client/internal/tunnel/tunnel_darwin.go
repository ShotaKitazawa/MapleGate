//go:build darwin

package tunnel

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/ShotaKitazawa/MapleGate/client/internal/netcfg"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"
)

// stateFile remembers the one route that outlives the interface, so a run killed with SIGKILL is cleaned up by the next.
const stateFile = "/var/run/gamevpn-tunnel.json"

type pinned struct {
	Endpoint string         `json:"endpoint"`
	Gateway  netcfg.Gateway `json:"gateway"`
}

func sh(args []string) (string, error) {
	out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	if args[0] == "route" && netcfg.RouteFailed(string(out)) {
		return string(out), fmt.Errorf("%s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func defaultGateway(skip string) (netcfg.Gateway, error) {
	out, err := sh([]string{"netstat", "-rn", "-f", "inet"})
	if err != nil {
		return netcfg.Gateway{}, err
	}
	return netcfg.ParseDefault(out, skip)
}

func dropStale() {
	b, err := os.ReadFile(stateFile)
	if err != nil {
		return
	}
	var p pinned
	if json.Unmarshal(b, &p) == nil && p.Endpoint != "" {
		_, _ = sh(netcfg.EndpointRoute("delete", p.Endpoint, p.Gateway))
	}
	_ = os.Remove(stateFile)
}

func pin(endpoint string, gw netcfg.Gateway) error {
	if _, err := sh(netcfg.EndpointRoute("add", endpoint, gw)); err != nil {
		return err
	}
	b, _ := json.Marshal(pinned{Endpoint: endpoint, Gateway: gw})
	return os.WriteFile(stateFile, b, 0o600)
}

func unpin(endpoint string, gw netcfg.Gateway) {
	_, _ = sh(netcfg.EndpointRoute("delete", endpoint, gw))
	_ = os.Remove(stateFile)
}

// Run holds the tunnel up until ctx is done or the device stops, and removes everything it added on the way out.
func Run(ctx context.Context, u Up, logf func(format string, a ...any), ready func(iface string)) (err error) {
	if err := u.Validate(); err != nil {
		return err
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("the tunnel needs root; run it with sudo")
	}
	epIP, err := netcfg.EndpointIP(u.Endpoint)
	if err != nil {
		return err
	}
	conf, err := u.UAPI()
	if err != nil {
		return err
	}
	dropStale()

	gw, err := defaultGateway("")
	if err != nil {
		return fmt.Errorf("find the current gateway: %w", err)
	}

	tdev, err := tun.CreateTUN("utun", MTU)
	if err != nil {
		return fmt.Errorf("create the interface: %w", err)
	}
	iface, err := tdev.Name()
	if err != nil {
		_ = tdev.Close()
		return err
	}
	dev := device.NewDevice(tdev, conn.NewDefaultBind(), device.NewLogger(device.LogLevelError, "wireguard: "))
	routes := netcfg.Routes(u.AllowedIPs)
	pinnedNow := false
	defer func() {
		for _, c := range netcfg.Down(iface, routes) {
			_, _ = sh(c)
		}
		if pinnedNow {
			unpin(epIP, gw)
		}
		dev.Close()
		logf("tunnel down, routes removed")
	}()

	if err := dev.IpcSet(conf); err != nil {
		return fmt.Errorf("configure wireguard: %w", err)
	}
	if err := dev.Up(); err != nil {
		return err
	}
	// the endpoint route goes in before the halves of the default route, or the tunnel's own packets would loop
	if err := pin(epIP, gw); err != nil {
		return err
	}
	pinnedNow = true
	cmds, err := netcfg.Up(iface, u.Address, routes)
	if err != nil {
		return err
	}
	for _, c := range cmds {
		if _, err := sh(c); err != nil {
			return err
		}
	}
	logf("tunnel up on %s via %s", iface, firstNonEmpty(gw.Via, gw.Iface))
	if ready != nil {
		ready(iface)
	}

	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-dev.Wait():
			return fmt.Errorf("the wireguard device stopped")
		case <-tick.C:
			now, err := defaultGateway(iface)
			if err != nil || now == gw {
				continue
			}
			logf("the gateway changed to %s, moving the route to the node", firstNonEmpty(now.Via, now.Iface))
			unpin(epIP, gw)
			pinnedNow = false
			if err := pin(epIP, now); err != nil {
				logf("could not move the route: %v", err)
				continue
			}
			gw, pinnedNow = now, true
		}
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
