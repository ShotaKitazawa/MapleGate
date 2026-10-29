// maplegated is the privileged half of the client: it holds the tunnel for the tray app, which is not privileged.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ShotaKitazawa/MapleGate/client/internal/daemon"
	"github.com/ShotaKitazawa/MapleGate/client/internal/ipc"
	"github.com/ShotaKitazawa/MapleGate/client/internal/tunnel"
)

func main() {
	sock := flag.String("socket", "/var/run/maplegated.sock", "path of the Unix socket")
	orphan := flag.Duration("orphan", 15*time.Minute, "take the tunnel down after this long without a call")
	flag.Parse()
	if err := run(*sock, *orphan); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(sock string, orphan time.Duration) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	_ = os.Remove(sock)
	l, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	// anyone may connect to the file; who is served is decided from the peer's uid
	if err := os.Chmod(sock, 0o666); err != nil {
		return err
	}
	defer os.Remove(sock)

	m := &daemon.Manager{Orphan: orphan,
		Run: func(ctx context.Context, u tunnel.Up, ready func(string)) error {
			return tunnel.Run(ctx, u, log.Printf, ready)
		}}
	go m.Watch(ctx, 30*time.Second)
	defer m.Down()

	s := &ipc.Server{Backend: m, Authorize: daemon.Allowed, PeerUID: daemon.PeerUID}
	log.Printf("listening on %s", sock)
	return s.Serve(ctx, l)
}
