//go:build !windows

package main

import (
	"net"
	"os"
)

const defaultSocket = "/var/run/maplegated.sock"

func isService() bool { return false }

func listen(sock string) (net.Listener, func(), error) {
	_ = os.Remove(sock)
	l, err := net.Listen("unix", sock)
	if err != nil {
		return nil, nil, err
	}
	// anyone may connect to the file; who is served is decided from the peer's uid
	if err := os.Chmod(sock, 0o666); err != nil {
		_ = l.Close()
		return nil, nil, err
	}
	return l, func() { os.Remove(sock) }, nil
}
