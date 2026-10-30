package app

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ShotaKitazawa/MapleGate/client/internal/api"
	"github.com/ShotaKitazawa/MapleGate/client/internal/auth"
	"github.com/ShotaKitazawa/MapleGate/client/internal/ipc"
	"github.com/ShotaKitazawa/MapleGate/client/internal/session"
	"github.com/stretchr/testify/assert"
)

func TestExplainIsOneShortSentenceForEachKnownFailure(t *testing.T) {
	cases := map[string]struct {
		err  error
		want string
	}{
		"denied":        {&session.EndedError{Status: "denied"}, "The request was denied."},
		"not approved":  {&session.EndedError{Status: "expired"}, noApproval},
		"node failed":   {&session.EndedError{Status: "failed", Detail: "no capacity"}, "The server could not start. Try again later."},
		"wait ran out":  {&session.WaitError{Status: "pending", Wait: time.Minute}, noApproval},
		"forbidden":     {&api.StatusError{Code: 403, Body: "x"}, "Not allowed. Ask the person who runs the server."},
		"busy":          {&api.StatusError{Code: 503}, "The server is busy. Try again in a minute."},
		"throttled":     {&api.StatusError{Code: 429}, "The server is busy. Try again in a minute."},
		"other status":  {&api.StatusError{Code: 400}, "The server refused the request."},
		"no service":    {fmt.Errorf("%w: %w", ipc.ErrServiceDown, errors.New("dial unix /var/run/maplegated.sock: connect: no such file")), "The VPN service is not running. Restart the computer."},
		"sign in late":  {auth.ErrSignInTimeout, "Sign in was not completed in time. Connect again."},
		"never seen":    {errors.New("operation error SSO: GetRoleCredentials, https response error StatusCode: 403, RequestID: 1234"), unknownFailure},
		"wrapped match": {fmt.Errorf("outer: %w", &api.StatusError{Code: 403}), "Not allowed. Ask the person who runs the server."},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, c.want, explain(c.err))
		})
	}
}

func TestEveryExplanationFitsAMenuLine(t *testing.T) {
	for _, s := range []string{noApproval, unknownFailure, endedBeforeUse("denied"), endedInUse("expired"), endedInUse("closed"), endedInUse("failed"), endedInUse("x")} {
		assert.LessOrEqual(t, len(s), 100, s)
	}
}
