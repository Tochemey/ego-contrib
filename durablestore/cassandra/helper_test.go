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
	"context"
	"log"
	"net"
	"os"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
	testcontainers "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

type TestContainer struct {
	container testcontainers.Container
	address   string
	keyspace  string
}

func NewTestContainer() *TestContainer {
	ctx := context.Background()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "cassandra:5.0.6",
			ExposedPorts: []string{"9042/tcp"},
			Env: map[string]string{
				"MAX_HEAP_SIZE":         "1G",
				"HEAP_NEWSIZE":          "256M",
				"CASSANDRA_CLUSTER_NAME": "test",
				"CASSANDRA_DC":          "dc1",
				"CASSANDRA_RACK":        "rack1",
			},
			WaitingFor: wait.ForListeningPort("9042/tcp").WithStartupTimeout(300 * time.Second),
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
	mappedPort, err := container.MappedPort(ctx, "9042/tcp")
	if err != nil {
		log.Fatalf("Could not get container port: %s", err)
	}
	hostAndPort := net.JoinHostPort(host, mappedPort.Port())

	if err := waitForCassandra(hostAndPort, 300*time.Second); err != nil {
		log.Fatalf("Could not connect to docker: %s", err)
	}

	containerInstance := new(TestContainer)
	containerInstance.container = container
	containerInstance.address = hostAndPort
	containerInstance.keyspace = "test_keyspace"

	err = containerInstance.CreateKeyspaceAndTable()
	if err != nil {
		log.Fatalf("Could not create keyspace and table: %s", err)
	}

	return containerInstance
}

func (c TestContainer) Cleanup() {
	ctx := context.Background()
	if err := c.container.Terminate(ctx); err != nil {
		log.Fatalf("Could not terminate container: %s", err)
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

func waitForCassandra(address string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		session, err := getCassandraClient(address, "")
		if err == nil {
			session.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		time.Sleep(2 * time.Second)
	}
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
