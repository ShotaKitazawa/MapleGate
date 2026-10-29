package netcfg

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// the table's words are localized (here: Japanese) but its rows are numbers, which is what the parser relies on
const routePrint = `===========================================================================
インターフェイス一覧
 12...00 15 5d aa bb cc ......Hyper-V Virtual Ethernet Adapter
===========================================================================

IPv4 ルート テーブル
===========================================================================
アクティブ ルート:
ネットワーク宛先        ネットマスク          ゲートウェイ       インターフェイス  メトリック
          0.0.0.0          0.0.0.0      192.168.1.1     192.168.1.50     25
          0.0.0.0          0.0.0.0      172.20.0.1      172.20.0.2     15
          0.0.0.0          0.0.0.0         リンク上      169.254.1.1     5
        127.0.0.0        255.0.0.0         リンク上         127.0.0.1    331
===========================================================================
`

func TestParseDefaultWindowsTakesTheCheapestRouteThroughAGateway(t *testing.T) {
	g, err := ParseDefaultWindows(routePrint, "")

	require.NoError(t, err)
	assert.Equal(t, Gateway{Via: "172.20.0.1", Iface: "172.20.0.2"}, g, "an on-link default has no gateway to pin the node's route to")
}

func TestParseDefaultWindowsSkipsTheTunnelItself(t *testing.T) {
	g, err := ParseDefaultWindows(routePrint, "172.20.0.2")

	require.NoError(t, err)
	assert.Equal(t, "192.168.1.1", g.Via)
}

func TestParseDefaultWindowsWithoutADefault(t *testing.T) {
	_, err := ParseDefaultWindows("nothing here", "")

	assert.Error(t, err)
}

func TestWindowsUpGivesTheAddressThenTheRoutes(t *testing.T) {
	cmds, err := WindowsUp("MapleGate", "10.66.0.2/32", []string{"0.0.0.0/1", "::/1"})

	require.NoError(t, err)
	assert.Equal(t, []string{"netsh", "interface", "ipv4", "set", "address", "name=MapleGate", "source=static", "address=10.66.0.2", "mask=255.255.255.255"}, cmds[0])
	assert.Contains(t, cmds, []string{"netsh", "interface", "ipv4", "add", "route", "prefix=0.0.0.0/1", "interface=MapleGate", "nexthop=0.0.0.0", "metric=1", "store=active"})
	assert.Contains(t, cmds, []string{"netsh", "interface", "ipv6", "add", "address", "interface=MapleGate", "address=" + ULA, "store=active"})
	assert.Contains(t, cmds, []string{"netsh", "interface", "ipv6", "add", "route", "prefix=::/1", "interface=MapleGate", "nexthop=::", "metric=1", "store=active"})
}

func TestWindowsEndpointRoute(t *testing.T) {
	gw := Gateway{Via: "192.168.1.1"}
	assert.Equal(t, []string{"route", "add", "1.2.3.4", "mask", "255.255.255.255", "192.168.1.1", "metric", "1"}, WindowsEndpointRoute("add", "1.2.3.4", gw))
	assert.Equal(t, []string{"route", "delete", "1.2.3.4", "mask", "255.255.255.255", "192.168.1.1"}, WindowsEndpointRoute("delete", "1.2.3.4", gw))
}

func TestWindowsDownRemovesTheRoutes(t *testing.T) {
	cmds := WindowsDown("MapleGate", []string{"0.0.0.0/1", "::/1"})

	assert.Equal(t, []string{"netsh", "interface", "ipv4", "delete", "route", "prefix=0.0.0.0/1", "interface=MapleGate", "nexthop=0.0.0.0", "store=active"}, cmds[0])
	assert.Len(t, cmds, 2)
}
