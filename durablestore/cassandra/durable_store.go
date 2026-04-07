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
	"context"
	"sync"

	"github.com/tochemey/ego/v4/egopb"
	"github.com/tochemey/ego/v4/persistence"
	"google.golang.org/protobuf/proto"
)

// DurableStore implements the DurableStore interface
// and helps persist durable states in a Cassandra database
type DurableStore struct {
	cluster   *cassandra
	connected bool
	mu        sync.Mutex
}

// enforce interface implementation
var _ persistence.StateStore = (*DurableStore)(nil)

func NewDurableStore(config *Config) *DurableStore {
	cluster := newCassandra(config)
	return &DurableStore{
		cluster: cluster,
	}
}

// Connect establishes a connection to the Cassandra cluster.
func (s *DurableStore) Connect(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.connected {
		return nil
	}

	if err := s.cluster.Connect(); err != nil {
		return err
	}

	s.connected = true
	return nil
}

// Disconnect closes the connection to the Cassandra cluster.
func (s *DurableStore) Disconnect(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.connected {
		return nil
	}

	if err := s.cluster.Disconnect(); err != nil {
		return err
	}

	s.connected = false
	return nil
}

// Ping verifies a connection to the database is still alive, establishing a connection if necessary.
func (s *DurableStore) Ping(ctx context.Context) error {
	s.mu.Lock()
	connected := s.connected
	s.mu.Unlock()

	if !connected {
		return s.Connect(ctx)
	}

	return nil
}

// WriteState persist durable state for a given persistenceID.
func (s *DurableStore) WriteState(_ context.Context, state *egopb.DurableState) error {
	bytea, err := proto.Marshal(state.GetResultingState())
	if err != nil {
		return err
	}
	manifest := string(state.GetResultingState().ProtoReflect().Descriptor().FullName())

	return s.cluster.WriteState(
		state.GetPersistenceId(),
		state.GetVersionNumber(),
		bytea,
		manifest,
		state.GetTimestamp(),
		state.GetShard(),
	)
}

// GetLatestState fetches the latest durable state
func (s *DurableStore) GetLatestState(_ context.Context, persistenceID string) (*egopb.DurableState, error) {
	result, err := s.cluster.GetLatestState(persistenceID)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}

	return result.ToDurableState()
}
