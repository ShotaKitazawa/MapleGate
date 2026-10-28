package main

import (
	"context"

	"github.com/ShotaKitazawa/MapleGate/lambda/internal/config"
	"github.com/ShotaKitazawa/MapleGate/lambda/internal/sessionapi"
	"github.com/aws/aws-lambda-go/events"
)

// lazyAuth defers the SSM read to the first request so init cannot fail on a missing token.
type lazyAuth struct{ params *config.Params }

func (a lazyAuth) Authenticate(req events.LambdaFunctionURLRequest) (string, bool) {
	token, err := a.params.Get(context.Background(), "session_api_token")
	if err != nil {
		return "", false
	}
	return sessionapi.FixedHeaderAuth{Token: token}.Authenticate(req)
}
