//go:build windows

package ipc

import (
	"net"
	"time"

	"github.com/Microsoft/go-winio"
)

func dial(path string) (net.Conn, error) {
	t := 3 * time.Second
	return winio.DialPipe(path, &t)
}
