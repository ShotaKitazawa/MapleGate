//go:build windows

package main

import (
	"net"

	"github.com/Microsoft/go-winio"
)

const defaultSocket = `\\.\pipe\maplegated`

// SYSTEM and administrators, and users signed in at a screen: the outer gate. Which of them is served is decided from the
// process on the other end, as on macOS.
const pipeACL = "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GRGW;;;IU)"

func listen(name string) (net.Listener, func(), error) {
	l, err := winio.ListenPipe(name, &winio.PipeConfig{SecurityDescriptor: pipeACL})
	if err != nil {
		return nil, nil, err
	}
	return l, func() { l.Close() }, nil
}
