// maplegated is the privileged half of the client: it holds the tunnel for the tray app, which is not privileged.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ShotaKitazawa/MapleGate/client/internal/daemon"
	"github.com/ShotaKitazawa/MapleGate/client/internal/ipc"
	"github.com/ShotaKitazawa/MapleGate/client/internal/tunnel"
)

// set by the Windows build, where the daemon is a service
var runAsService func(func(ctx context.Context) error) error

func main() {
	sock := flag.String("socket", defaultSocket, "where the tray app connects (a Unix socket path, or a pipe name on Windows)")
	orphan := flag.Duration("orphan", 15*time.Minute, "take the tunnel down after this long without a call")
	flag.Parse()

	var err error
	if runAsService != nil && isService() {
		err = runAsService(func(ctx context.Context) error { return run(ctx, *sock, *orphan) })
	} else {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		err = run(ctx, *sock, *orphan)
		stop()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, sock string, orphan time.Duration) error {
	l, cleanup, err := listen(sock)
	if err != nil {
		return err
	}
	defer cleanup()

	m := &daemon.Manager{Orphan: orphan,
		Run: func(ctx context.Context, u tunnel.Up, ready func(string)) error {
			return tunnel.Run(ctx, u, log.Printf, ready)
		}}
	go m.Watch(ctx, 30*time.Second)
	defer m.Down()

	s := &ipc.Server{Backend: m, Allow: daemon.AllowConn}
	log.Printf("listening on %s", sock)
	return s.Serve(ctx, l)
}
