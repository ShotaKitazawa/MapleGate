//go:build windows

package daemon

import (
	"net"
	"unsafe"

	"golang.org/x/sys/windows"
)

// AllowConn serves the user at the console, SYSTEM, and the account the daemon itself runs as. The pipe's access list
// is the outer gate; this is the same check as on macOS, made from the process on the other end of the pipe.
func AllowConn(c net.Conn) bool {
	f, ok := c.(interface{ Fd() uintptr })
	if !ok {
		return false
	}
	var pid uint32
	if err := windows.GetNamedPipeClientProcessId(windows.Handle(f.Fd()), &pid); err != nil {
		return false
	}
	peer, err := processUser(pid)
	if err != nil {
		return false
	}
	if system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid); err == nil && peer.Equals(system) {
		return true
	}
	if me, err := tokenUser(windows.GetCurrentProcessToken()); err == nil && peer.Equals(me) {
		return true
	}
	console, err := consoleUser()
	return err == nil && peer.Equals(console)
}

func tokenUser(t windows.Token) (*windows.SID, error) {
	u, err := t.GetTokenUser()
	if err != nil {
		return nil, err
	}
	return u.User.Sid, nil
}

func processUser(pid uint32) (*windows.SID, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(h)
	var t windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &t); err != nil {
		return nil, err
	}
	defer t.Close()
	return tokenUser(t)
}

var (
	wtsapi32                = windows.NewLazySystemDLL("wtsapi32.dll")
	procWTSQueryUserToken   = wtsapi32.NewProc("WTSQueryUserToken")
	kernel32                = windows.NewLazySystemDLL("kernel32.dll")
	procActiveConsoleSessID = kernel32.NewProc("WTSGetActiveConsoleSessionId")
)

// consoleUser is the user signed in at the machine. WTSQueryUserToken needs SYSTEM, which the service has; run from a
// terminal it fails and only the daemon's own account is served.
func consoleUser() (*windows.SID, error) {
	sess, _, _ := procActiveConsoleSessID.Call()
	var tok windows.Token
	if r, _, err := procWTSQueryUserToken.Call(sess, uintptr(unsafe.Pointer(&tok))); r == 0 {
		return nil, err
	}
	defer tok.Close()
	return tokenUser(tok)
}
