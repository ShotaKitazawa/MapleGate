package store_test

import (
	"context"
	"os"
	"testing"

	"github.com/ShotaKitazawa/MapleGate/internal/store"
	"github.com/ShotaKitazawa/MapleGate/internal/store/storetest"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/stretchr/testify/require"
)

// Set DYNAMODB_ENDPOINT (e.g. http://localhost:8000 from amazon/dynamodb-local) to run the contract against real conditional-write semantics.
func TestDynamoContract(t *testing.T) {
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
	storetest.Run(t, func(t *testing.T) storetest.Full {
		n++
		table := "gamevpn-test-" + string(rune('a'+n))
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
	})
}
