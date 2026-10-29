package diag

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestTailKeepsTheLastLines(t *testing.T) {
	assert.Equal(t, "c\nd", Tail("a\nb\nc\nd\n", 2))
	assert.Equal(t, "a", Tail("a\n", 5))
	assert.Equal(t, "", Tail("", 5))
}

func TestEventsKeepOnlyTheNewest(t *testing.T) {
	e := &Events{Max: 2, Now: func() time.Time { return time.Date(2026, 10, 29, 11, 0, 0, 0, time.UTC) }}

	e.Add("one")
	e.Add("two")
	e.Add("three")

	assert.Equal(t, []string{"2026-10-29T11:00:00Z two", "2026-10-29T11:00:00Z three"}, e.Lines())
}

func TestReportHasEverySectionEvenWhenOneIsEmpty(t *testing.T) {
	out := Build(Input{Version: "0.1.0", System: "macOS 15 arm64", Menu: "Connected · 5m", Daemon: "state=up", Events: []string{"x"}})

	for _, h := range []string{"MapleGate 0.1.0", "System", "Menu", "Daemon", "Settings", "Routes", "Daemon log", "Recent events"} {
		assert.Contains(t, out, h)
	}
	assert.True(t, strings.HasSuffix(out, "\n"))
}

func TestRoutesDropOtherDevicesOnTheLAN(t *testing.T) {
	in := "default            192.168.0.1        UGScg                 en0\n" +
		"192.168.0.11       70:85:c2:80:79:28  UHLWIi                en0   1031\n" +
		"128.0/1            utun8              USc                 utun8\n"

	assert.Equal(t, "default            192.168.0.1        UGScg                 en0\n128.0/1            utun8              USc                 utun8", Routes(in))
}
