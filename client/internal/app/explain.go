package app

import (
	"errors"
	"net/http"

	"github.com/ShotaKitazawa/MapleGate/client/internal/api"
	"github.com/ShotaKitazawa/MapleGate/client/internal/auth"
	"github.com/ShotaKitazawa/MapleGate/client/internal/ipc"
	"github.com/ShotaKitazawa/MapleGate/client/internal/session"
)

const unknownFailure = "Could not connect. Use Copy diagnostics and send it to the person who runs the server."

const noApproval = "Nobody approved in time. Connect again."

// explain is what the menu and the notice say; the error itself goes to the log.
func explain(err error) string {
	var ended *session.EndedError
	var slow *session.WaitError
	var status *api.StatusError
	switch {
	case errors.As(err, &ended):
		return endedBeforeUse(ended.Status)
	case errors.As(err, &slow):
		return noApproval
	case errors.As(err, &status):
		switch {
		case status.Code == http.StatusForbidden:
			return "Not allowed. Ask the person who runs the server."
		case status.Code == http.StatusTooManyRequests || status.Code >= 500:
			return "The server is busy. Try again in a minute."
		}
		return "The server refused the request."
	case errors.Is(err, ipc.ErrServiceDown):
		return "The VPN service is not running. Restart the computer."
	case errors.Is(err, auth.ErrSignInTimeout):
		return "Sign in was not completed in time. Connect again."
	}
	return unknownFailure
}

func endedBeforeUse(status string) string {
	switch status {
	case "denied":
		return "The request was denied."
	case "expired":
		return noApproval
	case "failed":
		return "The server could not start. Try again later."
	}
	return "The request ended. Connect again."
}

func endedInUse(status string) string {
	switch status {
	case "expired":
		return "The session reached its time limit. Connect again."
	case "closed":
		return "The session was closed."
	case "failed":
		return "The server stopped working. Connect again."
	}
	return "The session ended."
}
