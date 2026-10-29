//go:build !windows

package main

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/ShotaKitazawa/MapleGate/client/internal/app"
	"github.com/ShotaKitazawa/MapleGate/client/internal/diag"
	"github.com/ShotaKitazawa/MapleGate/client/internal/gamewatch"
)

const (
	daemonSocket = "/var/run/maplegated.sock"
	daemonLog    = "/var/log/maplegated.log"
)

// trayIcon is the PNG as it is: the macOS menu bar takes it.
func trayIcon(png []byte, _ int) []byte { return png }

func notify(title, msg string) {
	script := fmt.Sprintf("display notification %q with title %q", msg, title)
	_ = exec.Command("osascript", "-e", script).Run()
}

func openURL(u string) error { return exec.Command("open", u).Run() }

func openLog() { _ = exec.Command("open", "-a", "Console", daemonLog).Run() }

func copyToClipboard(text string) error {
	c := exec.Command("pbcopy")
	c.Stdin = strings.NewReader(text)
	return c.Run()
}

func systemInfo() string {
	return strings.TrimSpace(output("sw_vers", "-productVersion")) + " " + runtime.GOARCH
}

func routesText() string {
	return diag.Routes(output("netstat", "-rn", "-f", "inet")) + "\n" + diag.Routes(output("netstat", "-rn", "-f", "inet6"))
}

// gameRunning reads the executable paths with ps: pgrep and nettop cut a name at 16 characters, which hits this game's.
func gameRunning(names []string) (bool, error) {
	out, err := exec.Command("ps", "-axo", "comm=").Output()
	if err != nil {
		return false, err
	}
	return gamewatch.Running(names, strings.Split(string(out), "\n")), nil
}

// askToLeave gives up after the same two minutes the watcher waits, so a dialog nobody sees does not stay behind; a cancelled ctx kills it.
func askToLeave(ctx context.Context) app.Answer {
	script := `display dialog "The game has not been running for a while. Disconnect MapleGate?" with title "MapleGate" ` +
		`buttons {"Disconnect", "Keep connected"} default button "Keep connected" giving up after 120`
	out, err := exec.CommandContext(ctx, "osascript", "-e", script).Output()
	switch {
	case err != nil, strings.Contains(string(out), "gave up:true"):
		return app.NoAnswer
	case strings.Contains(string(out), "button returned:Disconnect"):
		return app.Leave
	}
	return app.Keep
}

// output is what a command prints, or why it could not run; a diagnostic must not stop at the first thing that fails.
func output(name string, args ...string) string {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Sprintf("%s: %v", name, err)
	}
	return string(out)
}
