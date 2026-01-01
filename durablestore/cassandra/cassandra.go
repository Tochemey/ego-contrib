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

package cassandra

import (
	"fmt"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

type cassandra struct {
	cluster *gocql.ClusterConfig
	session *gocql.Session
	config  *Config
}

// newDatabase returns a store connecting to the given database
func newCassandra(config *Config) *cassandra {
	cluster := gocql.NewCluster(config.Cluster)
	cluster.Keyspace = config.Keyspace
	cluster.Consistency = config.Consistency
	return &cassandra{
		cluster: cluster,
	}
}

func (c *cassandra) Connect() error {
	session, err := c.cluster.CreateSession()
	if err != nil {
		return err
	}
	c.session = session
	return nil
}

func (c *cassandra) Disconnect() error {
	c.session.Close()
	return nil
}

func (c *cassandra) WriteState(persistenceID string, versionNumber uint64, bytea []byte, manifest string, timestamp int64, shardNumber uint64) error {
	err := c.session.Query(
		`INSERT INTO states_store (persistence_id, version_number, state_payload, state_manifest, timestamp, shard_number) VALUES (?, ?, ?, ?, ?, ?)`,
		persistenceID, versionNumber, bytea, manifest, timestamp, shardNumber,
	).Consistency(c.cluster.Consistency).Exec()
	if err != nil {
		return fmt.Errorf("failed to write state to cassandra: %w", err)
	}

	return nil
}

func (c *cassandra) GetLatestState(persistenceID string) (*row, error) {
	// var state *row
	state := row{}
	if err := c.session.Query(
		`SELECT persistence_id, version_number, state_payload, state_manifest, timestamp, shard_number FROM states_store WHERE persistence_id = ?`,
		persistenceID,
	).
		Consistency(c.cluster.Consistency).
		Scan(
			&state.PersistenceID,
			&state.VersionNumber,
			&state.StatePayload,
			&state.StateManifest,
			&state.Timestamp,
			&state.ShardNumber,
		); err != nil {
		return nil, fmt.Errorf("failed to read latest state from cassandra: %w", err)
	}
	return &state, nil
}
