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
	"fmt"

	"github.com/tochemey/ego/v3/egopb"
	"github.com/tochemey/ego/v3/persistence"
	"go.uber.org/atomic"
	"google.golang.org/protobuf/proto"
)

// DurableStore implements the DurableStore interface
// and helps persist durable states in a Cassandra database
type DurableStore struct {
	cluster   *cassandra
	connected *atomic.Bool
}

// enforce interface implementation
var _ persistence.StateStore = (*DurableStore)(nil)

func NewDurableStore(config *Config) *DurableStore {
	cluster := newCassandra(config)
	return &DurableStore{
		cluster:   cluster,
		connected: atomic.NewBool(false),
	}
}

// Connect connects to the journal store
// No connection is needed because the client is stateless
func (s *DurableStore) Connect(_ context.Context) error {
	if s.connected.Load() {
		return nil
	}

	if err := s.cluster.Connect(); err != nil {
		fmt.Printf("Failed to connect to Cassandra: %v", err)
		return err
	}

	s.connected.Store(true)

	return nil
}

// Disconnect disconnect the journal store
// There is no need to disconnect because the client is stateless
func (s *DurableStore) Disconnect(_ context.Context) error {
	if !s.connected.Load() {
		return nil
	}

	if err := s.cluster.Disconnect(); err != nil {
		return err
	}

	s.connected.Store(false)

	return nil
}

// Ping verifies a connection to the database is still alive, establishing a connection if necessary.
// There is no need to ping because the client is stateless
func (s *DurableStore) Ping(ctx context.Context) error {
	if !s.connected.Load() {
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
	if result == nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	state, err := result.ToDurableState()
	if err != nil {
		return nil, err
	}

	return state, nil
}
