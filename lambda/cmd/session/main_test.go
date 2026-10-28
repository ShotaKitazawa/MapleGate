package main

import (
	"context"
	"testing"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/stretchr/testify/require"
)

func TestHandlerSignatureIsAcceptedByTheLambdaRuntime(t *testing.T) {
	// the sessionapi.Handler is never reached: an unauthenticated request is rejected before any store access
	out, err := lambda.NewHandler(handler(nil)).Invoke(context.Background(), []byte(`{`))
	require.NotContains(t, string(out), "does not implement error")
	require.NotContains(t, errString(err), "does not implement error")
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
