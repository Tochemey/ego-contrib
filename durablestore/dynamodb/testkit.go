package dynamodb

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	dockertest "github.com/ory/dockertest/v3"
	"github.com/ory/dockertest/v3/docker"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type TestContainer struct {
	resource *dockertest.Resource
	address  string
}

func NewTestContainer() *TestContainer {
	// Create a new dockertest pool
	pool, err := dockertest.NewPool("")
	if err != nil {
		log.Fatalf("Could not connect to Docker: %s", err)
	}

	// Run a DynamoDB local container
	resource, err := pool.RunWithOptions(&dockertest.RunOptions{
		Repository: "amazon/dynamodb-local",
		Tag:        "2.5.4",
	}, func(config *docker.HostConfig) {
		// set AutoRemove to true so that stopped container goes away by itself
		config.AutoRemove = true
		config.RestartPolicy = docker.RestartPolicy{Name: "no"}
	})
	hostAndPort := resource.GetHostPort("8000/tcp")

	if err != nil {
		log.Fatalf("Could not start resource: %s", err)
	}

	if err = pool.Retry(func() error {
		resp, err := http.Get(fmt.Sprintf("http://%s/", hostAndPort))
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		// Check if the status code is 400 which means the server is responding
		if resp.StatusCode != http.StatusBadRequest {
			return err
		}

		return nil
	}); err != nil {
		log.Fatalf("Could not connect to docker: %s", err)
	}

	// Tell docker to hard kill the container in 120 seconds
	_ = resource.Expire(300)
	pool.MaxWait = 120 * time.Second

	container := new(TestContainer)
	container.resource = resource
	container.address = hostAndPort

	return container
}

func (c TestContainer) GetDdbStore() *DynamoDurableStore {
	cfg, _ := config.LoadDefaultConfig(context.Background(),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("fakekey", "fakesecret", "")),
		config.WithRegion("us-east-1"),
	)

	// Create an DynamoDB client with the BaseEndpoint set to DynamoDB Local
	client := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		o.BaseEndpoint = aws.String(fmt.Sprintf("http://%s", c.address))
	})

	tableName := "states_store"
	store := NewDurableStore(tableName, client)
	c.CreateTable(tableName, client)
	return store
}

func (c TestContainer) CreateTable(tableName string, client *dynamodb.Client) error {
	_, err := client.CreateTable(context.TODO(), &dynamodb.CreateTableInput{
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
