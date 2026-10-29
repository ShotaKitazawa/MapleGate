//go:build windows

package main

import (
	"context"
	"log"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/svc"
)

const serviceName = "MapleGate"

func init() {
	runAsService = func(run func(ctx context.Context) error) error {
		dir := filepath.Join(os.Getenv("ProgramData"), "MapleGate")
		_ = os.MkdirAll(dir, 0o755)
		// a service has no console, so the log goes to a file the tray's "Show log" can open
		if f, err := os.OpenFile(filepath.Join(dir, "maplegated.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			log.SetOutput(f)
		}
		return svc.Run(serviceName, handler{run})
	}
}

func isService() bool {
	ok, err := svc.IsWindowsService()
	return err == nil && ok
}

type handler struct {
	run func(ctx context.Context) error
}

func (h handler) Execute(_ []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.run(ctx) }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case err := <-done:
			if err != nil {
				log.Printf("stopped: %v", err)
				return false, 1
			}
			return false, 0
		case c := <-req:
			switch c.Cmd {
			case svc.Interrogate:
				status <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				<-done
				return false, 0
			}
		}
	}
}
