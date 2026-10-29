// Package presign makes S3 GET links with the SDK's signer; the S3 client would add a module for one call.
package presign

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

const unsignedPayload = "UNSIGNED-PAYLOAD"

func GetURL(ctx context.Context, creds aws.CredentialsProvider, region, bucket, key string, ttl time.Duration, now time.Time) (string, error) {
	c, err := creds.Retrieve(ctx)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s?X-Amz-Expires=%d", bucket, region, key, int(ttl.Seconds())), nil)
	if err != nil {
		return "", err
	}
	u, _, err := v4.NewSigner().PresignHTTP(ctx, c, req, unsignedPayload, "s3", region, now)
	return u, err
}
