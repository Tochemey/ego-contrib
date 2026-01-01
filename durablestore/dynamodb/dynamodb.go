// MIT License
//
// Copyright (c) 2024-2026 Arsene Tochemey Gandote
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package dynamodb

import (
	"context"
	"fmt"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type database interface {
	// Upsert item in DynamoDB
	UpsertItem(ctx context.Context, item *item) error
	// Query data based on the key supplied in DynamoDB
	GetItem(ctx context.Context, key string) (*item, error)
}

type ddb struct {
	tableName string
	client    *dynamodb.Client
}

var _ database = (*ddb)(nil)

func newDynamodb(tableName string, client *dynamodb.Client) database {
	return ddb{
		client:    client,
		tableName: tableName,
	}
}

func (ddb ddb) GetItem(ctx context.Context, persistenceID string) (*item, error) {
	key := map[string]types.AttributeValue{
		"PersistenceID": &types.AttributeValueMemberS{Value: persistenceID},
	}

	result, err := ddb.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(ddb.tableName),
		Key:       key,
	})

	switch {
	case err != nil:
		return nil, fmt.Errorf("failed to fetch the state from the dynamodb: %w", err)
	case result == nil:
		return nil, fmt.Errorf("failed to fetch the state from the dynamodb")
	case result.Item == nil:
		return nil, nil
	default:
		return &item{
			PersistenceID: persistenceID,
			VersionNumber: parseDynamoUint64(result.Item["VersionNumber"]),
			StatePayload:  result.Item["StatePayload"].(*types.AttributeValueMemberB).Value,
			StateManifest: result.Item["StateManifest"].(*types.AttributeValueMemberS).Value,
			Timestamp:     parseDynamoInt64(result.Item["Timestamp"]),
			ShardNumber:   parseDynamoUint64(result.Item["ShardNumber"]),
		}, nil
	}
}

func (ddb ddb) UpsertItem(ctx context.Context, item *item) error {
	_, err := ddb.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(ddb.tableName),
		Item: map[string]types.AttributeValue{
			"PersistenceID": &types.AttributeValueMemberS{Value: item.PersistenceID}, // Partition key
			"VersionNumber": &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", item.VersionNumber)},
			"StatePayload":  &types.AttributeValueMemberB{Value: item.StatePayload},
			"StateManifest": &types.AttributeValueMemberS{Value: item.StateManifest},
			"Timestamp":     &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", item.Timestamp)},
			"ShardNumber":   &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", item.ShardNumber)},
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
