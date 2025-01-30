package dynamodb

import (
	"log"
	"os"
	"time"

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

	// Run a LocalStack container
	resource, err := pool.RunWithOptions(&dockertest.RunOptions{
		Repository: "localstack/localstack",
		Tag:        "4",
		Env: []string{
			"SERVICES=dynamodb",
			"DEBUG=1",
		},
	}, func(config *docker.HostConfig) {
		// set AutoRemove to true so that stopped container goes away by itself
		config.AutoRemove = true
		config.RestartPolicy = docker.RestartPolicy{Name: "no"}
	})

	if err != nil {
		log.Fatalf("Could not start resource: %s", err)
	}

	// Clean up the container after tests
	defer func() {
		if err := pool.Purge(resource); err != nil {
			log.Fatalf("Could not purge resource: %s", err)
		}
	}()

	// Tell docker to hard kill the container in 120 seconds
	_ = resource.Expire(120)
	pool.MaxWait = 120 * time.Second

	// Wait for LocalStack to be ready
	if err := pool.Retry(func() error {
		// Check if the container is accessible
		_, err := os.Open("http://localhost:" + resource.GetPort("4566/tcp"))
		return err
	}); err != nil {
		log.Fatalf("Could not connect to LocalStack: %s", err)
	}
	log.Println("LocalStack is running!")

	container := new(TestContainer)
	container.pool = pool
	container.resource = resource
	container.address = resource.GetHostPort("4566/tcp")

	return container

}
