package store_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ShotaKitazawa/MapleGate/internal/store"
	"github.com/ShotaKitazawa/MapleGate/internal/store/storetest"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newDynamo(t *testing.T) (*dynamodb.Client, func() *store.Dynamo) {
	endpoint := os.Getenv("DYNAMODB_ENDPOINT")
	if endpoint == "" {
		t.Skip("DYNAMODB_ENDPOINT not set")
	}
	client := dynamodb.New(dynamodb.Options{
		Region:       "us-west-2",
		BaseEndpoint: aws.String(endpoint),
		Credentials:  credentials.NewStaticCredentialsProvider("x", "x", ""),
	})
	n := 0
	return client, func() *store.Dynamo {
		n++
		table := fmt.Sprintf("gamevpn-test-%s-%d", strings.ReplaceAll(t.Name(), "/", "-"), n)
		_, err := client.CreateTable(context.Background(), &dynamodb.CreateTableInput{
			TableName: &table,
			KeySchema: []types.KeySchemaElement{
				{AttributeName: aws.String("PK"), KeyType: types.KeyTypeHash},
				{AttributeName: aws.String("SK"), KeyType: types.KeyTypeRange},
			},
			AttributeDefinitions: []types.AttributeDefinition{
				{AttributeName: aws.String("PK"), AttributeType: types.ScalarAttributeTypeS},
				{AttributeName: aws.String("SK"), AttributeType: types.ScalarAttributeTypeS},
			},
			BillingMode: types.BillingModePayPerRequest,
		})
		require.NoError(t, err)
		t.Cleanup(func() { _, _ = client.DeleteTable(context.Background(), &dynamodb.DeleteTableInput{TableName: &table}) })
		return &store.Dynamo{Client: client, Table: table}
	}
}

// Set DYNAMODB_ENDPOINT (e.g. http://localhost:8000 from amazon/dynamodb-local) to run the contract against real conditional-write semantics.
func TestDynamoContract(t *testing.T) {
	_, fresh := newDynamo(t)
	storetest.Run(t, func(t *testing.T) storetest.Full { return fresh() })
}

// The in-memory store has no clock, so what happens when a lease runs out is only checked here.
func TestDynamoLeaseExpiryAndRenewal(t *testing.T) {
	_, fresh := newDynamo(t)
	ctx := context.Background()
	d := fresh()
	past := time.Now().Add(-3 * time.Hour)

	require.NoError(t, d.LeaseIP(ctx, "10.66.0.2", "s1", past))
	require.NoError(t, d.LeaseIP(ctx, "10.66.0.2", "s2", time.Now().Add(time.Hour)), "an expired lease can be taken: this is what an unrenewed session in use would suffer")

	require.NoError(t, d.LeaseIP(ctx, "10.66.0.3", "s1", past))
	require.NoError(t, d.RenewLease(ctx, "10.66.0.3", "s1", time.Now().Add(time.Hour)))
	assert.ErrorIs(t, d.LeaseIP(ctx, "10.66.0.3", "s2", time.Now().Add(time.Hour)), store.ErrConflict, "a renewed lease is held again")
}
