package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"time"

	"fyne.io/systray"
	"github.com/ShotaKitazawa/MapleGate/client/internal/api"
	"github.com/ShotaKitazawa/MapleGate/client/internal/app"
	"github.com/ShotaKitazawa/MapleGate/client/internal/ipc"
	"github.com/ShotaKitazawa/MapleGate/client/internal/session"
)

const daemonSocket = "/var/run/maplegated.sock"

// dot draws the tray icon: a filled circle, because the state is all it has to say.
func dot(c color.RGBA) []byte {
	const n = 22
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			dx, dy := float64(x)-10.5, float64(y)-10.5
			if dx*dx+dy*dy <= 8*8 {
				img.Set(x, y, c)
			}
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

var icons = map[string][]byte{
	app.Idle:      dot(color.RGBA{128, 128, 128, 255}),
	app.Waiting:   dot(color.RGBA{230, 160, 0, 255}),
	app.Connected: dot(color.RGBA{40, 170, 70, 255}),
	app.Failed:    dot(color.RGBA{210, 50, 50, 255}),
}

func notify(title, msg string) {
	script := fmt.Sprintf("display notification %q with title %q", msg, title)
	_ = exec.Command("osascript", "-e", script).Run()
}

func tray() error {
	var a *app.App
	s, l, err := newLogin(func(m string) {
		if a != nil {
			a.Say(m)
		}
	})
	if err != nil {
		return err
	}
	l.Prompt = func(_, code string) { a.Say("Finish signing in in your browser (code " + code + ")") }
	client := &api.Client{Base: s.APIURL, Region: s.Region, Creds: l.Provider()}
	a = &app.App{
		Sessions: &session.Runner{API: client, Progress: func(m string) { a.Say(m) }},
		Daemon:   ipc.Client{Path: daemonSocket},
		Wait:     10 * time.Minute,
		Poll:     5 * time.Second,
		Forget:   l.Forget,
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
					_ = exec.Command("open", "-a", "Console", "/var/log/maplegated.log").Run()
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
