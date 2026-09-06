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

package postgres

import (
	"context"
	"errors"
	"fmt"
	"sync"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/ego/v4/egopb"
	"github.com/tochemey/ego/v4/persistence"
)

var (
	columns = []string{
		"persistence_id",
		"version_number",
		"state_payload",
		"state_manifest",
		"timestamp",
		"shard_number",
	}

	tableName = "states_store"

	// errNotConnected is returned when an operation runs against a store that is not connected
	errNotConnected = errors.New("durable store is not connected")
)

// DurableStore implements the persistence.StateStore interface
// and helps persist durable states in a postgres database
type DurableStore struct {
	// config holds the settings used to build the pool.
	// It is only set when the store owns its pool.
	config *Config
	// pool runs the queries. It is either built from config in Connect
	// or supplied by the caller through NewDurableStoreWithPool.
	pool Pool
	// owned is the pool built by the store from config.
	// It is nil when the pool was supplied by the caller, in which case the store never closes it.
	owned *pgxpool.Pool
	sb    sq.StatementBuilderType
	// guards connection state transitions
	mu        sync.Mutex
	connected bool
}

// enforce interface implementation
var _ persistence.StateStore = (*DurableStore)(nil)

// NewDurableStore creates a durable store that builds and owns its own connection pool.
// The pool is opened by Connect and closed by Disconnect.
func NewDurableStore(config *Config) *DurableStore {
	return &DurableStore{
		config: config.sanitize(),
		sb:     sq.StatementBuilder.PlaceholderFormat(sq.Dollar),
	}
}

// NewDurableStoreWithPool creates a durable store backed by a pool supplied by the caller.
// The caller keeps ownership of the pool: Connect only verifies it is reachable and
// Disconnect never closes it, so the same pool can be shared with other stores
// and with the rest of the application.
func NewDurableStoreWithPool(pool Pool) *DurableStore {
	return &DurableStore{
		pool: pool,
		sb:   sq.StatementBuilder.PlaceholderFormat(sq.Dollar),
	}
}

// Connect connects to the underlying postgres database.
// When the store owns its pool, the pool is created here. When the pool was supplied
// by the caller, Connect only verifies the database is reachable.
func (s *DurableStore) Connect(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.connected {
		return nil
	}

	if s.config != nil {
		pool, err := newPool(ctx, s.config)
		if err != nil {
			return err
		}
		s.owned = pool
		s.pool = pool
		s.connected = true
		return nil
	}

	if s.pool == nil {
		return errors.New("durable store pool is not defined")
	}

	if err := s.pool.Ping(ctx); err != nil {
		return fmt.Errorf("failed to ping the database connection: %w", err)
	}

	s.connected = true
	return nil
}

// Disconnect disconnects from the underlying postgres database.
// The pool is only closed when it was built by the store.
func (s *DurableStore) Disconnect(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.connected {
		return nil
	}

	if s.owned != nil {
		s.owned.Close()
		s.owned = nil
		s.pool = nil
	}

	s.connected = false
	return nil
}

// Ping verifies a connection to the database is still alive, establishing a connection if necessary.
func (s *DurableStore) Ping(ctx context.Context) error {
	pool, err := s.activePool()
	if err != nil {
		return s.Connect(ctx)
	}
	return pool.Ping(ctx)
}

// activePool returns the pool to run queries against, or an error when the store is not connected
func (s *DurableStore) activePool() (Pool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.connected {
		return nil, errNotConnected
	}
	return s.pool, nil
}

// WriteState writes a durable state into the underlying postgres database
func (s *DurableStore) WriteState(ctx context.Context, state *egopb.DurableState) error {
	pool, err := s.activePool()
	if err != nil {
		return err
	}

	if state == nil || proto.Equal(state, &egopb.DurableState{}) {
		return nil
	}

	bytea, err := proto.Marshal(state.GetResultingState())
	if err != nil {
		return fmt.Errorf("failed to marshal the durable state: %w", err)
	}
	manifest := string(state.GetResultingState().ProtoReflect().Descriptor().FullName())

	statement := s.sb.
		Insert(tableName).
		Columns(columns...).
		Values(
			state.GetPersistenceId(),
			state.GetVersionNumber(),
			bytea,
			manifest,
			state.GetTimestamp(),
			state.GetShard(),
		).Suffix("ON CONFLICT (persistence_id) " +
		"DO UPDATE SET " +
		"version_number = excluded.version_number," +
		"state_payload = excluded.state_payload, " +
		"state_manifest = excluded.state_manifest," +
		"timestamp = excluded.timestamp," +
		"shard_number = excluded.shard_number",
	)

	query, args, err := statement.ToSql()
	if err != nil {
		return fmt.Errorf("unable to build sql insert statement: %w", err)
	}

	if _, err := pool.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("failed to record durable state: %w", err)
	}

	return nil
}

// GetLatestState fetches the latest durable state of a persistenceID
func (s *DurableStore) GetLatestState(ctx context.Context, persistenceID string) (*egopb.DurableState, error) {
	pool, err := s.activePool()
	if err != nil {
		return nil, err
	}

	statement := s.sb.
		Select(columns...).
		From(tableName).
		Where(sq.Eq{"persistence_id": persistenceID})

	query, args, err := statement.ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build the select sql statement: %w", err)
	}

	row := new(row)
	if err := selectOne(ctx, pool, row, query, args...); err != nil {
		return nil, fmt.Errorf("failed to fetch the latest durable state from the database: %w", err)
	}

	if row.PersistenceID == "" {
		return nil, nil
	}

	return row.ToDurableState()
}
