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

package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net"
	"strconv"
	"time"

	_ "github.com/lib/pq" //nolint
	dockertest "github.com/ory/dockertest/v3"
	"github.com/ory/dockertest/v3/docker"
)

// TestContainer helps creates a database docker container to
// run unit tests
type TestContainer struct {
	host   string
	port   int
	schema string

	resource *dockertest.Resource
	pool     *dockertest.Pool

	// connection credentials
	dbUser string
	dbName string
	dbPass string
}

// NewTestContainer create a database test container useful for unit and integration tests
// This function will exit when there is an error.Call this function inside your SetupTest to create the container before each test.
func NewTestContainer(dbName, dbUser, dbPassword string) *TestContainer {
	// create the docker pool
	pool, err := dockertest.NewPool("")
	if err != nil {
		log.Fatalf("Could not connect to docker: %s", err)
	}
	// pulls an image, creates a container based on it and runs it
	resource, err := pool.RunWithOptions(&dockertest.RunOptions{
		Repository: "postgres",
		Tag:        "11",
		Env: []string{
			fmt.Sprintf("POSTGRES_PASSWORD=%s", dbPassword),
			fmt.Sprintf("POSTGRES_USER=%s", dbUser),
			fmt.Sprintf("POSTGRES_DB=%s", dbName),
			"listen_addresses = '*'",
		},
		Cmd: []string{
			"postgres", "-c", "log_statement=all", "-c", "log_connections=on", "-c", "log_disconnections=on",
		},
	}, func(config *docker.HostConfig) {
		// set AutoRemove to true so that stopped container goes away by itself
		config.AutoRemove = true
		config.RestartPolicy = docker.RestartPolicy{Name: "no"}
	})
	// handle the error
	if err != nil {
		log.Fatalf("Could not start resource: %s", err)
	}
	// get the host and port of the database connection
	hostAndPort := resource.GetHostPort("5432/tcp")
	databaseURL := fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable", dbUser, dbPassword, hostAndPort, dbName)
	// Tell docker to hard kill the container in 120 seconds
	_ = resource.Expire(120)
	// exponential backoff-retry, because the application in the container might not be ready to accept connections yet
	pool.MaxWait = 120 * time.Second

	if err = pool.Retry(func() error {
		db, err := sql.Open("postgres", databaseURL)
		if err != nil {
			return err
		}
		return db.Ping()
	}); err != nil {
		log.Fatalf("Could not connect to docker: %s", err)
	}

	// create an instance of TestContainer
	container := new(TestContainer)
	container.pool = pool
	container.resource = resource
	host, port, err := splitHostAndPort(hostAndPort)
	if err != nil {
		log.Fatalf("Unable to get database host and port: %s", err)
	}
	// set the container host, port and schema
	container.dbName = dbName
	container.dbUser = dbUser
	container.dbPass = dbPassword
	container.host = host
	container.port = port
	container.schema = "public"
	return container
}

// GetTestDB returns a database TestDB that can be used in the tests
// to perform some database queries
func (c TestContainer) GetTestDB() *TestDB {
	return &TestDB{
		newDatabase(&dbConfig{
			DBHost:                c.host,
			DBPort:                c.port,
			DBName:                c.dbName,
			DBUser:                c.dbUser,
			DBPassword:            c.dbPass,
			DBSchema:              c.schema,
			MaxConnections:        4,
			MinConnections:        0,
			MaxConnectionLifetime: time.Hour,
			MaxConnIdleTime:       30 * time.Minute,
			HealthCheckPeriod:     time.Minute,
		}),
	}
}

// Host return the host of the test container
func (c TestContainer) Host() string {
	return c.host
}

// Port return the port of the test container
func (c TestContainer) Port() int {
	return c.port
}

// Schema return the test schema of the test container
func (c TestContainer) Schema() string {
	return c.schema
}

// Cleanup frees the resource by removing a container and linked volumes from docker.
// Call this function inside your TearDownSuite to clean-up resources after each test
func (c TestContainer) Cleanup() {
	if err := c.pool.Purge(c.resource); err != nil {
		log.Fatalf("Could not purge resource: %s", err)
	}
}

// TestDB is used in test to perform
// some database queries
type TestDB struct {
	database
}

// DropTable utility function to drop a database table
func (c TestDB) DropTable(ctx context.Context, tableName string) error {
	var dropSQL = fmt.Sprintf("DROP TABLE IF EXISTS %s CASCADE;", tableName)
	_, err := c.Exec(ctx, dropSQL)
	return err
}

// TableExists utility function to help check the existence of table in database
// tableName is in the format: <schemaName.tableName>. e.g: public.users
func (c TestDB) TableExists(ctx context.Context, tableName string) error {
	var stmt = fmt.Sprintf("SELECT to_regclass('%s');", tableName)
	_, err := c.Exec(ctx, stmt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}

	return nil
}

// Count utility function to help count the number of rows in a database table.
// tableName is in the format: <schemaName.tableName>. e.g: public.users
// It returns -1 when there is an error
func (c TestDB) Count(ctx context.Context, tableName string) (int, error) {
	var count int
	if err := c.Select(ctx, &count, fmt.Sprintf("SELECT COUNT(*) FROM %s", tableName)); err != nil {
		return -1, err
	}
	return count, nil
}

// CreateSchema helps create a test schema in a database database
func (c TestDB) CreateSchema(ctx context.Context, schemaName string) error {
	stmt := fmt.Sprintf("CREATE SCHEMA %s", schemaName)
	if _, err := c.Exec(ctx, stmt); err != nil {
		return err
	}
	return nil
}

// SchemaExists helps check the existence of a database schema. Very useful when implementing tests
func (c TestDB) SchemaExists(ctx context.Context, schemaName string) (bool, error) {
	stmt := fmt.Sprintf("SELECT schema_name FROM information_schema.schemata WHERE schema_name = '%s';", schemaName)
	var check string
	if err := c.Select(ctx, &check, stmt); err != nil {
		return false, err
	}

	// this redundant check is necessary
	if check == schemaName {
		return true, nil
	}

	return false, nil
}

// DropSchema utility function to drop a database schema
func (c TestDB) DropSchema(ctx context.Context, schemaName string) error {
	var dropSQL = fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE;", schemaName)
	_, err := c.Exec(ctx, dropSQL)
	return err
}

// splitHostAndPort helps get the host address and port of and address
func splitHostAndPort(hostAndPort string) (string, int, error) {
	host, port, err := net.SplitHostPort(hostAndPort)
	if err != nil {
		return "", -1, err
	}

	portValue, err := strconv.Atoi(port)
	if err != nil {
		return "", -1, err
	}

	return host, portValue, nil
}
