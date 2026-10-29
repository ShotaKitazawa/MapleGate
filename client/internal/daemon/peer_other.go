//go:build !darwin && !windows

package daemon

import "net"

func AllowConn(net.Conn) bool { return false }
