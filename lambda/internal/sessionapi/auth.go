package sessionapi

import (
	"strings"

	"github.com/aws/aws-lambda-go/events"
)

type Authenticator interface {
	// Authenticate returns the caller's user name.
	Authenticate(req events.LambdaFunctionURLRequest) (string, bool)
}

const ssoRolePrefix = "AWSReservedSSO_"

// IAMAuth trusts the identity Lambda derived from the verified SigV4 signature. Only Identity Center
// sessions are accepted: an arbitrary role chooses its own session name, so it could claim to be anyone.
type IAMAuth struct{}

func (IAMAuth) Authenticate(req events.LambdaFunctionURLRequest) (string, bool) {
	a := req.RequestContext.Authorizer
	if a == nil || a.IAM == nil {
		return "", false
	}
	// arn:aws:sts::<account>:assumed-role/AWSReservedSSO_<permission set>_<hash>/<identity center user name>
	_, resource, ok := strings.Cut(a.IAM.UserARN, ":assumed-role/")
	if !ok {
		return "", false
	}
	role, user, ok := strings.Cut(resource, "/")
	if !ok || !strings.HasPrefix(role, ssoRolePrefix) || user == "" || strings.Contains(user, "/") {
		return "", false
	}
	return user, true
}

// SSOCaller builds the request context Lambda would attach for an Identity Center user.
func SSOCaller(user string) *events.LambdaFunctionURLRequestContextAuthorizerDescription {
	return &events.LambdaFunctionURLRequestContextAuthorizerDescription{
		IAM: &events.LambdaFunctionURLRequestContextAuthorizerIAMDescription{
			UserARN: "arn:aws:sts::123456789012:assumed-role/AWSReservedSSO_Player_0123456789abcdef/" + user,
		},
	}
}
