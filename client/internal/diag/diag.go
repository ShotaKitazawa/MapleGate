// Package diag builds the text a person pastes into a message when something goes wrong. It holds no secret: no token, key or password goes in.
package diag

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

type Input struct {
	Version  string
	System   string
	Menu     string
	Daemon   string
	Settings string
	Routes   string
	Log      string
	Events   []string
}

func Build(in Input) string {
	var b strings.Builder
	fmt.Fprintf(&b, "MapleGate %s\n", in.Version)
	for _, s := range []struct{ name, body string }{
		{"System", in.System}, {"Menu", in.Menu}, {"Daemon", in.Daemon}, {"Settings", in.Settings},
		{"Routes", in.Routes}, {"Daemon log", in.Log}, {"Recent events", strings.Join(in.Events, "\n")},
	} {
		fmt.Fprintf(&b, "\n== %s ==\n%s\n", s.name, strings.TrimRight(s.body, "\n"))
	}
	return b.String()
}

// Tail returns the last n lines of s.
func Tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// Events remembers the newest lines the tray said, since the tray has no log file of its own.
type Events struct {
	Max int
	Now func() time.Time

	mu    sync.Mutex
	lines []string
}

func (e *Events) Add(msg string) {
	now := time.Now
	if e.Now != nil {
		now = e.Now
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lines = append(e.lines, now().UTC().Format(time.RFC3339)+" "+msg)
	if len(e.lines) > e.Max {
		e.lines = e.lines[len(e.lines)-e.Max:]
	}
}

func (e *Events) Lines() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.lines...)
}

// Routes keeps the first 40 lines and drops the neighbour entries (flags L and W): they carry the MAC addresses of other devices on the person's network, and say nothing about the tunnel.
func Routes(s string) string {
	var keep []string
	for _, l := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		f := strings.Fields(l)
		if len(f) >= 3 && strings.Contains(f[2], "L") && strings.Contains(f[2], "W") {
			continue
		}
		keep = append(keep, l)
	}
	if len(keep) > 40 {
		keep = keep[:40]
	}
	return strings.Join(keep, "\n")
}
