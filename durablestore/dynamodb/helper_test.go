/*
 * MIT License
 *
 * Copyright (c) 2024-2025 Arsene Tochemey Gandote
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package dynamodb

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	testcontainers "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

type TestContainer struct {
	container testcontainers.Container
	address   string
}

func NewTestContainer() *TestContainer {
	ctx := context.Background()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "amazon/dynamodb-local:3.1.0",
			ExposedPorts: []string{"8000/tcp"},
			WaitingFor: wait.ForHTTP("/").
				WithPort("8000/tcp").
				WithStatusCodeMatcher(func(status int) bool {
					return status == http.StatusBadRequest
				}).
				WithStartupTimeout(120 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		log.Fatalf("Could not start container: %s", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		log.Fatalf("Could not get container host: %s", err)
	}
	mappedPort, err := container.MappedPort(ctx, "8000/tcp")
	if err != nil {
		log.Fatalf("Could not get container port: %s", err)
	}
	hostAndPort := net.JoinHostPort(host, mappedPort.Port())

	containerInstance := new(TestContainer)
	containerInstance.container = container
	containerInstance.address = hostAndPort

	return containerInstance
}

func (c TestContainer) Cleanup() {
	ctx := context.Background()
	if err := c.container.Terminate(ctx); err != nil {
		log.Fatalf("Could not terminate container: %s", err)
	}
}

func (c TestContainer) GetDdbClient(ctx context.Context) *dynamodb.Client {
	cfg, _ := config.LoadDefaultConfig(ctx,
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("fakekey", "fakesecret", "")),
		config.WithRegion("us-east-1"),
	)

	// Create an DynamoDB client with the BaseEndpoint set to DynamoDB Local
	return dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		o.BaseEndpoint = aws.String(fmt.Sprintf("http://%s", c.address))
	})
}

func (c TestContainer) GetDurableStore() *DynamoDurableStore {
	ctx := context.Background()
	client := c.GetDdbClient(ctx)

	tableName := "states_store"
	store := NewDurableStore(tableName, client)
	c.CreateTable(ctx, tableName, client)
	return store
}

func (c TestContainer) CreateTable(ctx context.Context, tableName string, client *dynamodb.Client) error {
	_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String(tableName),
		AttributeDefinitions: []types.AttributeDefinition{
			{
				AttributeName: aws.String("PersistenceID"),
				AttributeType: types.ScalarAttributeTypeS,
			},
		},
		KeySchema: []types.KeySchemaElement{
			{
				AttributeName: aws.String("PersistenceID"),
				KeyType:       types.KeyTypeHash,
			},
		},
		BillingMode: types.BillingModePayPerRequest,
	})

	return err
}
