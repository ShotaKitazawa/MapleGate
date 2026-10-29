// Package tunnel brings up a WireGuard interface for one session and takes it down again.
package tunnel

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

// Up is what the privileged side receives; Phase 5 sends the same object to the daemon over a socket.
type Up struct {
	PrivateKey string   `json:"private_key"`
	Address    string   `json:"address"`
	ServerKey  string   `json:"server_public_key"`
	Endpoint   string   `json:"endpoint"`
	AllowedIPs []string `json:"allowed_ips"`
	Keepalive  int      `json:"persistent_keepalive"`
}

// MTU leaves room for WireGuard's overhead on a path that may itself be 1500 or less.
const MTU = 1420

func (u Up) Validate() error {
	for name, v := range map[string]string{"private_key": u.PrivateKey, "server_public_key": u.ServerKey} {
		if _, err := wgtypes.ParseKey(v); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	if u.Address == "" || u.Endpoint == "" || len(u.AllowedIPs) == 0 {
		return errors.New("address, endpoint and allowed_ips are required")
	}
	return nil
}

// UAPI renders the WireGuard userspace configuration. Keys are hex there, not base64.
func (u Up) UAPI() (string, error) {
	priv, err := wgtypes.ParseKey(u.PrivateKey)
	if err != nil {
		return "", err
	}
	pub, err := wgtypes.ParseKey(u.ServerKey)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "private_key=%s\nreplace_peers=true\npublic_key=%s\nendpoint=%s\n",
		hex.EncodeToString(priv[:]), hex.EncodeToString(pub[:]), u.Endpoint)
	if u.Keepalive > 0 {
		fmt.Fprintf(&b, "persistent_keepalive_interval=%d\n", u.Keepalive)
	}
	for _, a := range u.AllowedIPs {
		fmt.Fprintf(&b, "allowed_ip=%s\n", a)
	}
	return b.String(), nil
}
