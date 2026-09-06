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

package slatedb

import (
	"context"
	"fmt"

	"github.com/tochemey/ego/v4/egopb"
	"github.com/tochemey/ego/v4/persistence"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/ego-contrib/durablestore/slatedb/internal/slatedbkit"
)

// keyPrefix is the SlateDB key prefix for durable state records.
const keyPrefix = "state/"

// DurableStore implements the persistence.StateStore interface on top of
// SlateDB. Each persistenceID maps to a single key under the "state/" prefix.
type DurableStore struct {
	store *slatedbkit.Store
}

// enforce interface implementation
var _ persistence.StateStore = (*DurableStore)(nil)

// NewDurableStore creates a SlateDB-backed durable state store.
func NewDurableStore(config *Config) *DurableStore {
	return &DurableStore{
		store: slatedbkit.NewStore(config.toKitConfig()),
	}
}

// Connect connects to the underlying SlateDB store.
func (s *DurableStore) Connect(ctx context.Context) error {
	return s.store.Connect(ctx)
}

// Disconnect disconnects from the underlying SlateDB store.
func (s *DurableStore) Disconnect(ctx context.Context) error {
	return s.store.Disconnect(ctx)
}

// Ping verifies a connection to the store is still alive, establishing a
// connection if necessary.
func (s *DurableStore) Ping(ctx context.Context) error {
	return s.store.Ping(ctx)
}

// WriteState persists the durable state for a given persistenceID as an
// upsert keyed on the persistenceID.
func (s *DurableStore) WriteState(_ context.Context, state *egopb.DurableState) error {
	if err := s.store.IsConnected(); err != nil {
		return err
	}

	if state == nil || proto.Equal(state, &egopb.DurableState{}) {
		return nil
	}

	value, err := proto.Marshal(state)
	if err != nil {
		return fmt.Errorf("failed to marshal durable state: %w", err)
	}

	handle, err := s.store.Db().Put(stateKey(state.GetPersistenceId()), value)
	if err != nil {
		return fmt.Errorf("failed to write durable state: %w", err)
	}

	if err := s.store.AwaitDurable(handle); err != nil {
		return fmt.Errorf("failed to await durable write: %w", err)
	}

	return nil
}

// GetLatestState fetches the latest durable state of a persistenceID.
func (s *DurableStore) GetLatestState(_ context.Context, persistenceID string) (*egopb.DurableState, error) {
	if err := s.store.IsConnected(); err != nil {
		return nil, err
	}

	raw, err := s.store.Db().Get(stateKey(persistenceID))
	if err != nil {
		return nil, fmt.Errorf("failed to fetch durable state: %w", err)
	}

	if raw == nil {
		return nil, nil
	}

	state := new(egopb.DurableState)
	if err := proto.Unmarshal(*raw, state); err != nil {
		return nil, fmt.Errorf("failed to unmarshal durable state: %w", err)
	}

	return state, nil
}

// stateKey builds the SlateDB key for a given persistenceID.
func stateKey(persistenceID string) []byte {
	return append([]byte(keyPrefix), []byte(slatedbkit.KeyID(persistenceID))...)
}
