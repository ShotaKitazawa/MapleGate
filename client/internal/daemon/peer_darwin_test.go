//go:build darwin

package daemon

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPeerUIDIsTheUIDOfTheProcessOnTheOtherEnd(t *testing.T) {
	d, err := os.MkdirTemp("/tmp", "gv")
	require.NoError(t, err)
	defer os.RemoveAll(d)
	l, err := net.Listen("unix", filepath.Join(d, "s"))
	require.NoError(t, err)
	defer l.Close()
	go func() {
		c, err := net.Dial("unix", l.Addr().String())
		if err == nil {
			defer c.Close()
			<-make(chan struct{})
		}
	}()

	c, err := l.Accept()
	require.NoError(t, err)
	defer c.Close()
	uid, err := PeerUID(c)

	require.NoError(t, err)
	assert.Equal(t, uint32(os.Getuid()), uid)
}
