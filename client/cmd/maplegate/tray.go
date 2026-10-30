package main

import (
	"encoding/json"
	"fmt"
	"image/color"
	"os"
	"time"

	"fyne.io/systray"
	"github.com/ShotaKitazawa/MapleGate/client/internal/api"
	"github.com/ShotaKitazawa/MapleGate/client/internal/app"
	"github.com/ShotaKitazawa/MapleGate/client/internal/diag"
	"github.com/ShotaKitazawa/MapleGate/client/internal/icon"
	"github.com/ShotaKitazawa/MapleGate/client/internal/ipc"
	"github.com/ShotaKitazawa/MapleGate/client/internal/session"
	"github.com/ShotaKitazawa/MapleGate/client/internal/settings"
)

// 32 pixels because the menu bar draws it 16 points high, twice that on a Retina screen
func dot(c color.RGBA) []byte { return trayIcon(icon.Dot(32, c), 32) }

var icons = map[string][]byte{
	app.Idle:      dot(color.RGBA{128, 128, 128, 255}),
	app.Waiting:   dot(color.RGBA{230, 160, 0, 255}),
	app.Connected: dot(color.RGBA{40, 170, 70, 255}),
	app.Failed:    dot(color.RGBA{210, 50, 50, 255}),
}

func tray() error {
	var a *app.App
	events := &diag.Events{Max: 60}
	s, l, err := newLogin(func(m string) {
		events.Add(m)
		if a != nil {
			a.Say(m)
		}
	})
	if err != nil {
		return err
	}
	l.Prompt = func(_, code string) {
		events.Add("sign in code " + code)
		a.Say("Finish signing in in your browser (code " + code + ")")
	}
	client := &api.Client{Base: s.APIURL, Region: s.Region, Creds: l.Provider()}
	a = &app.App{
		Sessions: &session.Runner{API: client, Progress: func(m string) { events.Add(m); a.Say(m) }},
		Daemon:   ipc.Client{Path: daemonSocket},
		Wait:     10 * time.Minute,
		Poll:     5 * time.Second,
		Forget:   l.Forget,
	}
	if len(s.GameProcesses) > 0 {
		a.Game = &app.Game{
			Running: func() (bool, error) { return gameRunning(s.GameProcesses) },
			Ask:     askToLeave,
			Absent:  10 * time.Minute,
			Answer:  2 * time.Minute,
		}
	}

	systray.Run(func() {
		systray.SetTooltip("MapleGate")
		status := systray.AddMenuItem("Not connected", "")
		status.Disable()
		systray.AddSeparator()
		connect := systray.AddMenuItem("Connect", "")
		disconnect := systray.AddMenuItem("Disconnect", "")
		disconnect.Disable()
		systray.AddSeparator()
		signOut := systray.AddMenuItem("Sign out", "")
		showLog := systray.AddMenuItem("Show log", "")
		copyDiag := systray.AddMenuItem("Copy diagnostics", "")
		systray.AddSeparator()
		quit := systray.AddMenuItem("Quit", "")

		label := func(st app.State) string {
			if st.Text == "" {
				st.Text = "Not connected"
				if !l.SignedIn() {
					st.Text = "Not signed in"
				}
			}
			return st.Label(time.Now())
		}
		a.OnChange = func(st app.State) {
			line := st.Phase + ": " + st.Text
			if st.Detail != "" {
				line += " (" + st.Detail + ")"
			}
			events.Add(line)
			systray.SetIcon(icons[st.Phase])
			status.SetTitle(label(st))
			if l.SignedIn() {
				signOut.Enable()
			} else {
				signOut.Disable()
			}
			busy := st.Phase == app.Waiting || st.Phase == app.Connected
			if busy {
				connect.Disable()
				disconnect.Enable()
			} else {
				connect.Enable()
				disconnect.Disable()
			}
			switch st.Phase {
			case app.Connected:
				notify("MapleGate", "Connected")
			case app.Failed:
				notify("MapleGate", st.Text)
			case app.Idle:
				// only a disconnect the person did not ask for carries a reason
				if st.Text != "" {
					notify("MapleGate", st.Text)
				}
			}
		}
		systray.SetIcon(icons[app.Idle])
		a.Adopt()

		go func() {
			// the time shown next to Connected has to move on its own
			for range time.Tick(time.Minute) {
				status.SetTitle(label(a.State()))
			}
		}()
		go func() {
			for {
				select {
				case <-connect.ClickedCh:
					a.Connect()
				case <-disconnect.ClickedCh:
					a.Disconnect()
				case <-showLog.ClickedCh:
					openLog()
				case <-copyDiag.ClickedCh:
					if err := copyToClipboard(diagnostics(s, l.SignedIn(), label(a.State()), events)); err != nil {
						notify("MapleGate", "Could not copy: "+err.Error())
					} else {
						notify("MapleGate", "Diagnostics copied. Paste them into your message.")
					}
				case <-signOut.ClickedCh:
					a.SignOut()
				case <-quit.ClickedCh:
					// a crash leaves the tunnel to the daemon's idle rule, but a deliberate Quit must not leave one nobody can see
					status.SetTitle("Disconnecting...")
					a.Disconnect()
					systray.Quit()
					return
				}
			}
		}()
	}, func() { fmt.Fprintln(os.Stderr, "bye") })
	return nil
}

func diagnostics(s settings.Settings, signedIn bool, menu string, events *diag.Events) string {
	var daemon string
	if st, err := (ipc.Client{Path: daemonSocket}).Do(ipc.Request{Op: "status"}); err != nil {
		daemon = "unreachable: " + err.Error()
	} else {
		daemon = fmt.Sprintf("state=%s iface=%s session=%s since=%s error=%q", st.State, st.Iface, st.SessionID, st.Since.Format(time.RFC3339), st.Error)
	}
	conf, _ := json.MarshalIndent(s, "", "  ")
	return diag.Build(diag.Input{
		Version:  version,
		System:   systemInfo(),
		Menu:     fmt.Sprintf("%s (signed in: %t)", menu, signedIn),
		Daemon:   daemon,
		Settings: string(conf),
		Routes:   routesText(),
		Log:      diag.Tail(readFile(daemonLog), 80),
		Events:   events.Lines(),
	})
}

func readFile(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return err.Error()
	}
	return string(b)
}
