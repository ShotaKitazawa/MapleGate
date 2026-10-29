package netcfg

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// The planning for Windows lives beside the macOS one and is pure too, so it is tested here without a Windows machine.

// ParseDefaultWindows reads `route print -4 0.0.0.0` and returns the cheapest default route that has a gateway and is not
// on skipIfaceIP (the tunnel's own address). Its words are localized but its rows are numbers; an on-link row has a word
// where the gateway is, and has nothing to pin the node's route to.
func ParseDefaultWindows(out, skipIfaceIP string) (Gateway, error) {
	best, bestMetric := Gateway{}, -1
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) < 5 || f[0] != "0.0.0.0" || f[1] != "0.0.0.0" || f[3] == skipIfaceIP {
			continue
		}
		if _, err := netip.ParseAddr(f[2]); err != nil {
			continue
		}
		m, err := strconv.Atoi(f[4])
		if err != nil {
			continue
		}
		if bestMetric < 0 || m < bestMetric {
			best, bestMetric = Gateway{Via: f[2], Iface: f[3]}, m
		}
	}
	if bestMetric < 0 {
		return Gateway{}, fmt.Errorf("no default route with a gateway found")
	}
	return best, nil
}

// WindowsEndpointRoute pins the node's address to the original gateway so the tunnel's own packets do not loop into it.
func WindowsEndpointRoute(verb, endpointIP string, gw Gateway) []string {
	c := []string{"route", verb, endpointIP, "mask", "255.255.255.255", gw.Via}
	if verb == "add" {
		c = append(c, "metric", "1")
	}
	return c
}

func netshFamily(prefix string) (fam, nexthop string) {
	if strings.Contains(prefix, ":") {
		return "ipv6", "::"
	}
	return "ipv4", "0.0.0.0"
}

// WindowsUp lists the commands that give the interface its address and send the prefixes into it. store=active keeps
// them out of the registry, so a crash cannot leave a route behind that survives a reboot.
func WindowsUp(iface, address string, routes []string) ([]([]string), error) {
	p, err := netip.ParsePrefix(address)
	if err != nil {
		return nil, fmt.Errorf("address %q: %w", address, err)
	}
	cmds := [][]string{{"netsh", "interface", "ipv4", "set", "address", "name=" + iface, "source=static", "address=" + p.Addr().String(), "mask=255.255.255.255"}}
	for _, r := range routes {
		if f, _ := netshFamily(r); f == "ipv6" {
			// without an IPv6 address the interface refuses IPv6 routes; the node drops the traffic, so the value is arbitrary
			cmds = append(cmds, []string{"netsh", "interface", "ipv6", "add", "address", "interface=" + iface, "address=" + ULA, "store=active"})
			break
		}
	}
	for _, r := range routes {
		f, nh := netshFamily(r)
		cmds = append(cmds, []string{"netsh", "interface", f, "add", "route", "prefix=" + r, "interface=" + iface, "nexthop=" + nh, "metric=1", "store=active"})
	}
	return cmds, nil
}

// WindowsDown lists the commands that remove the routes; removing the adapter drops them too, this is for a failure in between.
func WindowsDown(iface string, routes []string) [][]string {
	var cmds [][]string
	for _, r := range routes {
		f, nh := netshFamily(r)
		cmds = append(cmds, []string{"netsh", "interface", f, "delete", "route", "prefix=" + r, "interface=" + iface, "nexthop=" + nh, "store=active"})
	}
	return cmds
}
