package dynamodb

import (
	"context"
	"fmt"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type database interface {
	// Upsert item in DynamoDB
	UpsertItem(ctx context.Context, item *StateItem) error
	// Query data based on the key supplied in DynamoDB
	GetItem(ctx context.Context, key string) (*StateItem, error)
}

type ddb struct {
	tableName string
	client *dynamodb.Client
}

var _ database = (*ddb)(nil)

func newDynamodb(tableName, region string, baseEndpoint *string) database {
	cfg, _ := config.LoadDefaultConfig(context.Background(),
		config.WithRegion(region),
	)

	var client *dynamodb.Client
	if baseEndpoint != nil {
		// Create an DynamoDB client with the BaseEndpoint set to DynamoDB Local
		client = dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
			o.BaseEndpoint = aws.String(*baseEndpoint)
		})
	} else {
		client = dynamodb.NewFromConfig(cfg)
	}

	ddb := new(ddb)
	ddb.client = client
	ddb.tableName = tableName

	return ddb
}

func (ddb ddb) GetItem(ctx context.Context, persistenceId string) (*StateItem, error) {
	key := map[string]types.AttributeValue{
		"PersistenceID": &types.AttributeValueMemberS{Value: persistenceId},
	}

	result, err := ddb.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(ddb.tableName),
		Key:       key,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch the latest state from the dynamodb: %w", err)
	}

	// Check if item exists
	if result.Item == nil {
		return nil, nil
	}

	return &StateItem{
		PersistenceID: persistenceId,
		StatePayload:  result.Item["StatePayload"].(*types.AttributeValueMemberB).Value,
		StateManifest: result.Item["StateManifest"].(*types.AttributeValueMemberS).Value,
		Timestamp:     parseDynamoInt64(result.Item["Timestamp"]),
	}, nil
}

func (ddb ddb) UpsertItem(ctx context.Context, item *StateItem) error {
	_, err := ddb.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(ddb.tableName),
		Item: map[string]types.AttributeValue{
			"PersistenceID": &types.AttributeValueMemberS{Value: item.PersistenceID}, // Partition key
			"StatePayload":  &types.AttributeValueMemberB{Value: item.StatePayload},
			"StateManifest": &types.AttributeValueMemberS{Value: item.StateManifest},
			"Timestamp":     &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", item.Timestamp)},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to upsert state into the dynamodb: %w", err)
	}

	return err
}

func parseDynamoUint64(element types.AttributeValue) uint64 {
	n, _ := strconv.ParseUint(element.(*types.AttributeValueMemberN).Value, 10, 64)
	return n
}

func parseDynamoInt64(element types.AttributeValue) int64 {
	n, _ := strconv.ParseInt(element.(*types.AttributeValueMemberN).Value, 10, 64)
	return n
}
