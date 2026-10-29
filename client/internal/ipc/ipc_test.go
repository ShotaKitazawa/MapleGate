package ipc

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/ShotaKitazawa/MapleGate/client/internal/tunnel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

type backend struct{ calls []string }

func (b *backend) Up(r Request) Status {
	b.calls = append(b.calls, "up "+r.SessionID)
	return Status{State: "connecting", SessionID: r.SessionID}
}
func (b *backend) Down() Status { b.calls = append(b.calls, "down"); return Status{State: "down"} }
func (b *backend) Status() Status {
	b.calls = append(b.calls, "status")
	return Status{State: "up", Iface: "utun3"}
}

// shortPath keeps the socket path under the 104 bytes macOS allows, which t.TempDir() does not.
func shortPath(t *testing.T) string {
	d, err := os.MkdirTemp("/tmp", "gv")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return filepath.Join(d, "s")
}

func serve(t *testing.T, authorize func(uint32) bool) (Client, *backend) {
	b := &backend{}
	l, err := net.Listen("unix", shortPath(t))
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s := &Server{Backend: b, Authorize: authorize, PeerUID: func(net.Conn) (uint32, error) { return 501, nil }}
	go func() { _ = s.Serve(ctx, l) }()
	return Client{Path: l.Addr().String()}, b
}

func validUp(t *testing.T) *tunnel.Up {
	a, _ := wgtypes.GeneratePrivateKey()
	b, _ := wgtypes.GeneratePrivateKey()
	return &tunnel.Up{PrivateKey: a.String(), ServerKey: b.PublicKey().String(), Address: "10.66.0.2/32",
		Endpoint: "1.2.3.4:51820", AllowedIPs: []string{"0.0.0.0/0"}}
}

func TestStatusAndDownReachTheBackend(t *testing.T) {
	c, b := serve(t, func(uint32) bool { return true })

	st, err := c.Do(Request{Op: "status"})
	require.NoError(t, err)
	assert.Equal(t, "utun3", st.Iface)
	st, err = c.Do(Request{Op: "down"})
	require.NoError(t, err)
	assert.Equal(t, "down", st.State)
	assert.Equal(t, []string{"status", "down"}, b.calls)
}

func TestUpPassesTheSessionIDOn(t *testing.T) {
	c, b := serve(t, func(uint32) bool { return true })

	st, err := c.Do(Request{Op: "up", Up: validUp(t), SessionID: "s1"})

	require.NoError(t, err)
	assert.Equal(t, "s1", st.SessionID)
	assert.Equal(t, []string{"up s1"}, b.calls)
}

func TestAnInvalidUpNeverReachesTheBackend(t *testing.T) {
	c, b := serve(t, func(uint32) bool { return true })
	bad := validUp(t)
	bad.PrivateKey = "nope"

	_, err := c.Do(Request{Op: "up", Up: bad})

	assert.Error(t, err)
	assert.Empty(t, b.calls)
}

func TestAPeerThatIsNotAllowedGetsNothingDone(t *testing.T) {
	c, b := serve(t, func(uid uint32) bool { return uid == 0 })

	_, err := c.Do(Request{Op: "down"})

	assert.ErrorContains(t, err, "not allowed")
	assert.Empty(t, b.calls)
}

func TestAnUnknownOpIsRefused(t *testing.T) {
	c, _ := serve(t, func(uint32) bool { return true })

	_, err := c.Do(Request{Op: "reboot"})

	assert.ErrorContains(t, err, "unknown op")
}

func TestAMissingServiceIsExplained(t *testing.T) {
	_, err := Client{Path: shortPath(t)}.Do(Request{Op: "status"})

	assert.ErrorContains(t, err, "not running")
}
