//go:build windows

package main

import (
	"context"
	"encoding/base64"
	"encoding/csv"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/ShotaKitazawa/MapleGate/client/internal/app"
	"github.com/ShotaKitazawa/MapleGate/client/internal/gamewatch"
	"github.com/ShotaKitazawa/MapleGate/client/internal/icon"
)

const daemonSocket = `\\.\pipe\maplegated`

var daemonLog = filepath.Join(os.Getenv("ProgramData"), "MapleGate", "maplegated.log")

func trayIcon(png []byte, size int) []byte { return icon.ICO(png, size) }

// command runs a program without the console window flashing up, which the tray app (a GUI program) would otherwise show.
func command(ctx context.Context, name string, args ...string) *exec.Cmd {
	c := exec.CommandContext(ctx, name, args...)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	return c
}

// powershell takes the text it shows through the environment, never spliced into the script, so a message cannot inject code.
func powershell(ctx context.Context, script string, env ...string) ([]byte, error) {
	c := command(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	c.Env = append(os.Environ(), env...)
	return c.Output()
}

func notify(title, msg string) {
	const script = `[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
$x = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02)
$t = $x.GetElementsByTagName('text')
$t.Item(0).AppendChild($x.CreateTextNode($env:MG_TITLE)) | Out-Null
$t.Item(1).AppendChild($x.CreateTextNode($env:MG_MSG)) | Out-Null
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe').Show([Windows.UI.Notifications.ToastNotification]::new($x))`
	_, _ = powershell(context.Background(), script, "MG_TITLE="+title, "MG_MSG="+msg)
}

func openURL(u string) error {
	return command(context.Background(), "rundll32.exe", "url.dll,FileProtocolHandler", u).Run()
}

func openLog() { _ = exec.Command("notepad.exe", daemonLog).Start() }

// The text goes in as base64: PowerShell decodes stdin with the console code page (932 on a Japanese Windows) before a
// script can switch it, and the environment has a size limit a diagnostics report can exceed.
func copyToClipboard(text string) error {
	c := command(context.Background(), "powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		"Set-Clipboard -Value ([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String([Console]::In.ReadToEnd())))")
	c.Stdin = strings.NewReader(base64.StdEncoding.EncodeToString([]byte(text)))
	return c.Run()
}

func systemInfo() string {
	return strings.TrimSpace(output("cmd.exe", "/c", "ver")) + " " + runtime.GOARCH
}

func routesText() string {
	lines := strings.Split(output("route.exe", "print"), "\n")
	if len(lines) > 80 {
		lines = lines[:80]
	}
	return strings.Join(lines, "\n")
}

// gameRunning lists the image names with tasklist. Names are compared whole and without regard to case, as Windows does.
func gameRunning(names []string) (bool, error) {
	out, err := command(context.Background(), "tasklist.exe", "/FO", "CSV", "/NH").Output()
	if err != nil {
		return false, err
	}
	rows, err := csv.NewReader(strings.NewReader(string(out))).ReadAll()
	if err != nil {
		return false, err
	}
	var images []string
	for _, r := range rows {
		if len(r) > 0 {
			images = append(images, strings.ToLower(r[0]))
		}
	}
	lower := make([]string, len(names))
	for i, n := range names {
		lower[i] = strings.ToLower(n)
	}
	return gamewatch.Running(lower, images), nil
}

// askToLeave gives up after the same two minutes the watcher waits; killing powershell closes the dialog it owns.
func askToLeave(ctx context.Context) app.Answer {
	const script = `(New-Object -ComObject WScript.Shell).Popup($env:MG_TEXT, 120, 'MapleGate', 4 + 32 + 256)`
	out, err := powershell(ctx, script, "MG_TEXT=The game has not been running for a while. Disconnect MapleGate?\n\nYes: disconnect. No: stay connected.")
	switch {
	case err != nil:
		return app.NoAnswer
	case strings.TrimSpace(string(out)) == "6":
		return app.Leave
	case strings.TrimSpace(string(out)) == "7":
		return app.Keep
	}
	return app.NoAnswer
}

func output(name string, args ...string) string {
	out, err := command(context.Background(), name, args...).CombinedOutput()
	if err != nil {
		return fmt.Sprintf("%s: %v", name, err)
	}
	return string(out)
}
