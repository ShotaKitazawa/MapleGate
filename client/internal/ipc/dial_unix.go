//go:build !windows

package ipc

import (
	"net"
	"time"
)

func dial(path string) (net.Conn, error) { return net.DialTimeout("unix", path, 3*time.Second) }
