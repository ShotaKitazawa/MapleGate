// Package netcfg plans the address and route changes of a full tunnel on macOS. Planning is pure; Apply runs it.
package netcfg

import (
	"fmt"
	"net"
	"net/netip"
	"strings"
)

// Gateway is where the machine's traffic leaves today. Via is empty for an on-link default.
type Gateway struct {
	Via   string
	Iface string
}

// ParseDefault reads `netstat -rn -f inet` and returns the first default route that is not through skip.
func ParseDefault(out, skip string) (Gateway, error) {
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) < 4 || f[0] != "default" || f[3] == skip {
			continue
		}
		g := Gateway{Iface: f[3]}
		if !strings.HasPrefix(f[1], "link#") && net.ParseIP(f[1]) != nil {
			g.Via = f[1]
		}
		return g, nil
	}
	return Gateway{}, fmt.Errorf("no default route found")
}

// Routes turns AllowedIPs into the prefixes to send into the tunnel. A default route is split in halves so the
// real default stays in place, and a full tunnel also claims all of IPv6, which the node drops, so nothing leaks.
func Routes(allowed []string) []string {
	var out []string
	full := false
	for _, a := range allowed {
		switch a {
		case "0.0.0.0/0":
			full = true
			out = append(out, "0.0.0.0/1", "128.0.0.0/1")
		case "::/0":
			out = append(out, "::/1", "8000::/1")
		default:
			out = append(out, a)
		}
	}
	if full && !contains(out, "::/1") {
		out = append(out, "::/1", "8000::/1")
	}
	return out
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func family(prefix string) string {
	if strings.Contains(prefix, ":") {
		return "-inet6"
	}
	return "-inet"
}

// EndpointRoute pins the node's address to the original gateway so the tunnel's own packets do not loop into it.
func EndpointRoute(verb, endpointIP string, gw Gateway) []string {
	if gw.Via != "" {
		return []string{"route", "-n", verb, "-inet", endpointIP + "/32", gw.Via}
	}
	return []string{"route", "-n", verb, "-inet", endpointIP + "/32", "-interface", gw.Iface}
}

// ULA is the unique-local address given to the interface so that IPv6 routes can point at it.
const ULA = "fd6d:6170:6c65::2/128"

// RouteFailed reports whether the output of route(8) is an error. It exits 0 even when it fails, so the exit
// status cannot be trusted.
func RouteFailed(out string) bool {
	return strings.Contains(out, "route: writing to routing socket")
}

// Up lists the commands that give the interface its address and send the prefixes into it.
func Up(iface, address string, routes []string) ([][]string, error) {
	p, err := netip.ParsePrefix(address)
	if err != nil {
		return nil, fmt.Errorf("address %q: %w", address, err)
	}
	cmds := [][]string{
		{"ifconfig", iface, "inet", address, p.Addr().String(), "alias"},
		{"ifconfig", iface, "up"},
	}
	for _, r := range routes {
		if family(r) == "-inet6" {
			// without an IPv6 address the interface refuses IPv6 routes; the node drops the traffic, so the value is arbitrary
			cmds = append(cmds, []string{"ifconfig", iface, "inet6", ULA, "alias"})
			break
		}
	}
	for _, r := range routes {
		cmds = append(cmds, []string{"route", "-n", "add", family(r), r, "-interface", iface})
	}
	return cmds, nil
}

// Down lists the commands that remove what Up added. Removing the interface drops its routes too; this is for the ones
// that outlive it.
func Down(iface string, routes []string) [][]string {
	var cmds [][]string
	for _, r := range routes {
		cmds = append(cmds, []string{"route", "-n", "delete", family(r), r, "-interface", iface})
	}
	return cmds
}

// EndpointIP extracts the IPv4 address of "host:port".
func EndpointIP(endpoint string) (string, error) {
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil {
		return "", err
	}
	a, err := netip.ParseAddr(host)
	if err != nil || !a.Is4() {
		return "", fmt.Errorf("endpoint %q is not an IPv4 address", endpoint)
	}
	return host, nil
}
