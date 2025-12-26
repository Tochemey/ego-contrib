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

package cassandra

import (
	"log"
	"os"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
	dockertest "github.com/ory/dockertest/v3"
	"github.com/ory/dockertest/v3/docker"
)

type TestContainer struct {
	resource *dockertest.Resource
	pool     *dockertest.Pool
	address  string
	keyspace string
}

func NewTestContainer() *TestContainer {
	// Create a new dockertest pool
	pool, err := dockertest.NewPool("")
	if err != nil {
		log.Fatalf("Could not connect to Docker: %s", err)
	}
	pool.MaxWait = 300 * time.Second

	// Run a Cassandra container
	resource, err := pool.RunWithOptions(&dockertest.RunOptions{
		Repository: "cassandra",
		Tag:        "5.0.6",
		Env: []string{
			"MAX_HEAP_SIZE=1G",  // Set the maximum heap size to 1GB
			"HEAP_NEWSIZE=256M", // Set the new heap size to 256MB
			"CASSANDRA_CLUSTER_NAME=test",
			"CASSANDRA_DC=dc1",
			"CASSANDRA_RACK=rack1",
		},
	}, func(config *docker.HostConfig) {
		// set AutoRemove to true so that stopped container goes away by itself
		config.AutoRemove = true
		config.RestartPolicy = docker.RestartPolicy{Name: "no"}
	})
	if err != nil {
		log.Fatalf("Could not start resource: %s", err)
	}

	hostAndPort := resource.GetHostPort("9042/tcp")

	if err = pool.Retry(func() error {
		// Try to connect to the Cassandra container
		// to make sure the server is ready
		_, err := getCassandraClient(hostAndPort, "")
		return err
	}); err != nil {
		log.Fatalf("Could not connect to docker: %s", err)
	}

	// Tell docker to hard kill the container in 300 seconds
	_ = resource.Expire(300)

	container := new(TestContainer)
	container.resource = resource
	container.pool = pool
	container.address = hostAndPort
	container.keyspace = "test_keyspace"

	err = container.CreateKeyspaceAndTable()
	if err != nil {
		log.Fatalf("Could not create keyspace and table: %s", err)
	}

	return container
}

func (c TestContainer) Cleanup() {
	if err := c.pool.Purge(c.resource); err != nil {
		log.Fatalf("Could not purge resource: %s", err)
	}
}

func getCassandraClient(address string, keyspace string) (*gocql.Session, error) {
	cluster := gocql.NewCluster(address)
	if keyspace != "" {
		cluster.Keyspace = keyspace
	}
	cluster.Timeout = 60 * time.Second
	cluster.Consistency = gocql.LocalOne
	session, err := cluster.CreateSession()
	if err != nil {
		return nil, err
	}
	return session, nil
}

func (c TestContainer) GetDurableStore() *DurableStore {
	return NewDurableStore(&Config{
		Cluster:     c.address,
		Keyspace:    c.keyspace,
		Consistency: gocql.LocalOne,
	})
}

func (c TestContainer) CreateKeyspaceAndTable() error {
	// create a keyspace
	session, err := getCassandraClient(c.address, "")
	if err != nil {
		log.Fatalf("Could not get Cassandra client: %s", err)
		return err
	}

	err = session.Query(`CREATE KEYSPACE IF NOT EXISTS test_keyspace
WITH replication = {
  'class': 'SimpleStrategy',
  'replication_factor': 1
};`).Exec()
	if err != nil {
		log.Fatalf("Could not create keyspace: %s", err)
		return err
	}
	session.Close()

	migration, err := os.ReadFile("resources/states_store.sql")
	if err != nil {
		log.Fatalf("Could not read migration file: %s", err)
	}

	// create a table on the keyspace
	session, err = getCassandraClient(c.address, c.keyspace)
	if err != nil {
		log.Fatalf("Could not get Cassandra client: %s", err)
		return err
	}

	err = session.Query(string(migration)).Exec()
	if err != nil {
		log.Fatalf("Could not create table: %s", err)
		return err
	}
	session.Close()

	return nil
}
