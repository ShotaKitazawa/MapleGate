//go:build darwin

package daemon

import (
	"errors"
	"net"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// PeerUID is the uid of the process on the other end of a Unix socket, taken from the kernel, not from the request.
func PeerUID(c net.Conn) (uint32, error) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return 0, errors.New("not a unix connection")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0, err
	}
	var uid uint32
	var cerr error
	if err := raw.Control(func(fd uintptr) {
		x, e := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if e != nil {
			cerr = e
			return
		}
		uid = x.Uid
	}); err != nil {
		return 0, err
	}
	return uid, cerr
}

// ConsoleOwner is the user sitting at the machine.
func ConsoleOwner() (uint32, error) {
	fi, err := os.Stat("/dev/console")
	if err != nil {
		return 0, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, errors.New("no owner for /dev/console")
	}
	return st.Uid, nil
}
