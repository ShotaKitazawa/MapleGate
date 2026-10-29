package presign

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetURL(t *testing.T) {
	creds := credentials.NewStaticCredentialsProvider("AKIDEXAMPLE", "secret", "token")
	got, err := GetURL(context.Background(), creds, "us-west-2", "bkt", "dist/MapleGate.pkg", 15*time.Minute, time.Unix(1_700_000_000, 0))
	require.NoError(t, err)

	u, err := url.Parse(got)
	require.NoError(t, err)
	assert.Equal(t, "bkt.s3.us-west-2.amazonaws.com", u.Host)
	assert.Equal(t, "/dist/MapleGate.pkg", u.Path)
	q := u.Query()
	assert.Equal(t, "900", q.Get("X-Amz-Expires"))
	assert.Equal(t, "20231114T221320Z", q.Get("X-Amz-Date"))
	assert.NotEmpty(t, q.Get("X-Amz-Signature"))
	assert.Equal(t, "token", q.Get("X-Amz-Security-Token"))
}
