package netcfg

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const netstat = `Routing tables

Internet:
Destination        Gateway            Flags               Netif Expire
default            192.168.1.1        UGScg                 en0
default            link#26            UCSIg             bridge100      !
127                127.0.0.1          UCS                   lo0
`

func TestParseDefaultTakesTheFirstDefaultRoute(t *testing.T) {
	g, err := ParseDefault(netstat, "utun9")

	require.NoError(t, err)
	assert.Equal(t, Gateway{Via: "192.168.1.1", Iface: "en0"}, g)
}

func TestParseDefaultSkipsTheTunnelItself(t *testing.T) {
	out := "default            10.66.0.1          UGScg                utun9\ndefault            192.168.1.1        UGScg                 en0\n"

	g, err := ParseDefault(out, "utun9")

	require.NoError(t, err)
	assert.Equal(t, "en0", g.Iface)
}

func TestParseDefaultOnLinkHasNoGateway(t *testing.T) {
	g, err := ParseDefault("default            link#12            UCSg                  en0\n", "")

	require.NoError(t, err)
	assert.Equal(t, Gateway{Iface: "en0"}, g)
}

func TestParseDefaultWithNoDefaultIsAnError(t *testing.T) {
	_, err := ParseDefault("127                127.0.0.1          UCS                   lo0\n", "")

	assert.Error(t, err)
}

func TestAFullTunnelKeepsTheRealDefaultRouteAndClaimsAllOfIPv6(t *testing.T) {
	assert.Equal(t, []string{"0.0.0.0/1", "128.0.0.0/1", "::/1", "8000::/1"}, Routes([]string{"0.0.0.0/0"}))
}

func TestANarrowedTunnelRoutesWhatItIsGiven(t *testing.T) {
	assert.Equal(t, []string{"52.0.0.0/8", "10.66.0.1/32"}, Routes([]string{"52.0.0.0/8", "10.66.0.1/32"}))
}

func TestAnExplicitIPv6DefaultIsNotDuplicated(t *testing.T) {
	assert.Equal(t, []string{"0.0.0.0/1", "128.0.0.0/1", "::/1", "8000::/1"}, Routes([]string{"0.0.0.0/0", "::/0"}))
}

func TestUpAddsTheAddressThenEveryRoute(t *testing.T) {
	cmds, err := Up("utun5", "10.66.0.2/32", []string{"0.0.0.0/1", "::/1"})

	require.NoError(t, err)
	assert.Equal(t, [][]string{
		{"ifconfig", "utun5", "inet", "10.66.0.2/32", "10.66.0.2", "alias"},
		{"ifconfig", "utun5", "up"},
		{"ifconfig", "utun5", "inet6", ULA, "alias"},
		{"route", "-n", "add", "-inet", "0.0.0.0/1", "-interface", "utun5"},
		{"route", "-n", "add", "-inet6", "::/1", "-interface", "utun5"},
	}, cmds)
}

func TestUpGivesTheInterfaceAnIPv6AddressBeforeAnIPv6Route(t *testing.T) {
	cmds, err := Up("utun5", "10.66.0.2/32", []string{"0.0.0.0/1", "::/1", "8000::/1"})

	require.NoError(t, err)
	assert.Equal(t, []string{"ifconfig", "utun5", "inet6", ULA, "alias"}, cmds[2])
	assert.Equal(t, []string{"route", "-n", "add", "-inet", "0.0.0.0/1", "-interface", "utun5"}, cmds[3])
	assert.Len(t, cmds, 6)
}

func TestRouteFailedSeesAnErrorThatCameWithExitStatusZero(t *testing.T) {
	assert.True(t, RouteFailed("route: writing to routing socket: Network is unreachable\nadd net ::/1: gateway utun8: Network is unreachable\n"))
	assert.False(t, RouteFailed("add net 0.0.0.0/1: gateway utun8\n"))
	assert.False(t, RouteFailed(""))
}

func TestUpRejectsABadAddress(t *testing.T) {
	_, err := Up("utun5", "nonsense", nil)

	assert.Error(t, err)
}

func TestDownDeletesWhatUpAdded(t *testing.T) {
	assert.Equal(t, [][]string{
		{"route", "-n", "delete", "-inet", "0.0.0.0/1", "-interface", "utun5"},
	}, Down("utun5", []string{"0.0.0.0/1"}))
}

func TestTheEndpointStaysOnTheOriginalGateway(t *testing.T) {
	assert.Equal(t, []string{"route", "-n", "add", "-inet", "1.2.3.4/32", "192.168.1.1"},
		EndpointRoute("add", "1.2.3.4", Gateway{Via: "192.168.1.1", Iface: "en0"}))
	assert.Equal(t, []string{"route", "-n", "delete", "-inet", "1.2.3.4/32", "-interface", "en0"},
		EndpointRoute("delete", "1.2.3.4", Gateway{Iface: "en0"}))
}

func TestEndpointIPMustBeIPv4(t *testing.T) {
	ip, err := EndpointIP("35.1.2.3:51820")
	require.NoError(t, err)
	assert.Equal(t, "35.1.2.3", ip)

	_, err = EndpointIP("example.com:51820")
	assert.Error(t, err)
	_, err = EndpointIP("[2001:db8::1]:51820")
	assert.Error(t, err)
}
