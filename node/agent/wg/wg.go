package wg

import (
	"fmt"
	"net"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

type Peer struct {
	PublicKey     string
	Address       string // host address without a prefix length
	LastHandshake time.Time
}

type Interface interface {
	Peers() ([]Peer, error)
	Set(publicKey, address string) error
	Remove(publicKey string) error
}

// Ctrl drives the kernel interface through wgctrl, which avoids parsing `wg` output.
type Ctrl struct {
	Device string
}

func (c Ctrl) Peers() ([]Peer, error) {
	cl, err := wgctrl.New()
	if err != nil {
		return nil, err
	}
	defer cl.Close()
	d, err := cl.Device(c.Device)
	if err != nil {
		return nil, err
	}
	out := make([]Peer, 0, len(d.Peers))
	for _, p := range d.Peers {
		peer := Peer{PublicKey: p.PublicKey.String(), LastHandshake: p.LastHandshakeTime}
		if len(p.AllowedIPs) == 1 {
			peer.Address = p.AllowedIPs[0].IP.String()
		} else {
			// anything other than a single address is not ours and gets removed
			peer.Address = "?"
		}
		out = append(out, peer)
	}
	return out, nil
}

func (c Ctrl) Set(publicKey, address string) error {
	key, err := wgtypes.ParseKey(publicKey)
	if err != nil {
		return err
	}
	ip := net.ParseIP(address)
	if ip == nil {
		return fmt.Errorf("invalid address %q", address)
	}
	return c.configure(wgtypes.PeerConfig{
		PublicKey:         key,
		ReplaceAllowedIPs: true,
		AllowedIPs:        []net.IPNet{{IP: ip, Mask: net.CIDRMask(32, 32)}},
	})
}

func (c Ctrl) Remove(publicKey string) error {
	key, err := wgtypes.ParseKey(publicKey)
	if err != nil {
		return err
	}
	return c.configure(wgtypes.PeerConfig{PublicKey: key, Remove: true})
}

func (c Ctrl) configure(p wgtypes.PeerConfig) error {
	cl, err := wgctrl.New()
	if err != nil {
		return err
	}
	defer cl.Close()
	return cl.ConfigureDevice(c.Device, wgtypes.Config{Peers: []wgtypes.PeerConfig{p}})
}
