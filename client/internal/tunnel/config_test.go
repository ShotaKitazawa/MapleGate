package tunnel

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

func valid(t *testing.T) Up {
	a, err := wgtypes.GeneratePrivateKey()
	require.NoError(t, err)
	b, err := wgtypes.GeneratePrivateKey()
	require.NoError(t, err)
	return Up{PrivateKey: a.String(), ServerKey: b.PublicKey().String(), Address: "10.66.0.2/32",
		Endpoint: "35.1.2.3:51820", AllowedIPs: []string{"0.0.0.0/0"}, Keepalive: 25}
}

func TestUAPIUsesHexKeysAndListsTheWholePeer(t *testing.T) {
	u := valid(t)
	priv, _ := wgtypes.ParseKey(u.PrivateKey)
	srv, _ := wgtypes.ParseKey(u.ServerKey)

	got, err := u.UAPI()

	require.NoError(t, err)
	assert.Equal(t, "private_key="+hex.EncodeToString(priv[:])+"\nreplace_peers=true\npublic_key="+hex.EncodeToString(srv[:])+
		"\nendpoint=35.1.2.3:51820\npersistent_keepalive_interval=25\nallowed_ip=0.0.0.0/0\n", got)
}

func TestValidateRejectsABadKeyAndMissingFields(t *testing.T) {
	u := valid(t)
	require.NoError(t, u.Validate())

	bad := u
	bad.PrivateKey = "nope"
	assert.Error(t, bad.Validate())

	empty := u
	empty.AllowedIPs = nil
	assert.Error(t, empty.Validate())
}
