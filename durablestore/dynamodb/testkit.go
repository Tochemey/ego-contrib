package dynamodb

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	dockertest "github.com/ory/dockertest/v3"
	"github.com/ory/dockertest/v3/docker"
)

type TestContainer struct {
	resource *dockertest.Resource
	pool     *dockertest.Pool
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

	// Define the health check function
	healthCheck := func() error {
		resp, err := http.Get(fmt.Sprintf("http://%s/", hostAndPort))
		if err != nil {
			log.Fatalf("Failed to make request: %v", err)
		}
		defer resp.Body.Close()

		// Check if the status code is 200
		if resp.StatusCode != http.StatusBadRequest {
			return err
		}

		return nil
	}

	time.Sleep(10 * time.Second)

	// Retry the health check until it succeeds or times out
	if err := pool.Retry(func() error {
		return healthCheck()
	}); err != nil {
		log.Fatalf("dynamodb local did not start in time: %s", err)
	}

	// Tell docker to hard kill the container in 120 seconds
	_ = resource.Expire(120)
	pool.MaxWait = 120 * time.Second

	container := new(TestContainer)
	container.pool = pool
	container.resource = resource
	container.address = hostAndPort

	return container
}

func (c TestContainer) GetDdbClient() *dynamodb.Client {
	url := fmt.Sprintf("http://%s", c.address)
	cfg, err := config.LoadDefaultConfig(context.TODO(),
		config.WithRegion("localhost"),
		config.WithEndpointResolver(aws.EndpointResolverFunc(
			func(_, _ string) (aws.Endpoint, error) {
				return aws.Endpoint{URL: url, SigningRegion: "localhost"}, nil
			})),
	)
	if err != nil {
		panic("Could not launch amazon/dynamodb-local container")
	}

	// Create DynamoDB client
	client := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		o.Credentials = credentials.NewStaticCredentialsProvider("fakekey", "fakesecret", "")
	})

	return client
}
