//go:build !darwin

package daemon

import (
	"errors"
	"net"
)

func PeerUID(net.Conn) (uint32, error) {
	return 0, errors.New("peer credentials are only implemented for macOS")
}

func ConsoleOwner() (uint32, error) { return 0, errors.New("not implemented") }
