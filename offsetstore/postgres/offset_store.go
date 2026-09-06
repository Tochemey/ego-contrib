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
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/ego/v4/egopb"
	"github.com/tochemey/ego/v4/offsetstore"
)

var (
	columns = []string{
		"projection_name",
		"shard_number",
		"current_offset",
		"timestamp",
	}

	tableName = "offsets_store"

	// errNotConnected is returned when an operation runs against a store that is not connected
	errNotConnected = errors.New("offset store is not connected")
)

// offsetRow represent the offset entry in the offset store
type offsetRow struct {
	// ProjectionName is the projection name
	ProjectionName string
	// Shard Number
	ShardNumber uint64
	// Value is the current offset
	CurrentOffset int64
	// Specifies the last update time
	Timestamp int64
}

// OffsetStore implements the offsetstore.OffsetStore interface
// and helps persist offsets in a postgres database
type OffsetStore struct {
	// config holds the settings used to build the pool.
	// It is only set when the store owns its pool.
	config *Config
	// pool runs the queries. It is either built from config in Connect
	// or supplied by the caller through NewOffsetStoreWithPool.
	pool Pool
	// owned is the pool built by the store from config.
	// It is nil when the pool was supplied by the caller, in which case the store never closes it.
	owned *pgxpool.Pool
	sb    sq.StatementBuilderType
	// guards connection state transitions
	mu        sync.Mutex
	connected bool
}

// ensure the complete implementation of the OffsetStore interface
var _ offsetstore.OffsetStore = (*OffsetStore)(nil)

// NewOffsetStore creates an offset store that builds and owns its own connection pool.
// The pool is opened by Connect and closed by Disconnect.
func NewOffsetStore(config *Config) *OffsetStore {
	return &OffsetStore{
		config: config.sanitize(),
		sb:     sq.StatementBuilder.PlaceholderFormat(sq.Dollar),
	}
}

// NewOffsetStoreWithPool creates an offset store backed by a pool supplied by the caller.
// The caller keeps ownership of the pool: Connect only verifies it is reachable and
// Disconnect never closes it, so the same pool can be shared with other stores
// and with the rest of the application.
func NewOffsetStoreWithPool(pool Pool) *OffsetStore {
	return &OffsetStore{
		pool: pool,
		sb:   sq.StatementBuilder.PlaceholderFormat(sq.Dollar),
	}
}

// Connect connects to the underlying postgres database.
// When the store owns its pool, the pool is created here. When the pool was supplied
// by the caller, Connect only verifies the database is reachable.
func (s *OffsetStore) Connect(ctx context.Context) error {
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
		return errors.New("offset store pool is not defined")
	}

	if err := s.pool.Ping(ctx); err != nil {
		return fmt.Errorf("failed to ping the database connection: %w", err)
	}

	s.connected = true
	return nil
}

// Disconnect disconnects from the underlying postgres database.
// The pool is only closed when it was built by the store.
func (s *OffsetStore) Disconnect(context.Context) error {
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
func (s *OffsetStore) Ping(ctx context.Context) error {
	pool, err := s.activePool()
	if err != nil {
		return s.Connect(ctx)
	}
	return pool.Ping(ctx)
}

// activePool returns the pool to run queries against, or an error when the store is not connected
func (s *OffsetStore) activePool() (Pool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.connected {
		return nil, errNotConnected
	}
	return s.pool, nil
}

// WriteOffset writes an offset into the offset store
func (s *OffsetStore) WriteOffset(ctx context.Context, offset *egopb.Offset) error {
	pool, err := s.activePool()
	if err != nil {
		return err
	}

	if offset == nil || proto.Equal(offset, new(egopb.Offset)) {
		return errors.New("offset record is not defined")
	}

	statement := s.sb.
		Insert(tableName).
		Columns(columns...).
		Values(
			offset.GetProjectionName(),
			offset.GetShardNumber(),
			offset.GetValue(),
			offset.GetTimestamp()).
		Suffix("ON CONFLICT (projection_name, shard_number) " +
			"DO UPDATE SET current_offset = EXCLUDED.current_offset, timestamp = EXCLUDED.timestamp")

	query, args, err := statement.ToSql()
	if err != nil {
		return fmt.Errorf("unable to build sql upsert statement: %w", err)
	}

	if _, err := pool.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("failed to write offset: %w", err)
	}

	return nil
}

// GetCurrentOffset returns the current offset of a given projection id
func (s *OffsetStore) GetCurrentOffset(ctx context.Context, projectionID *egopb.ProjectionId) (currentOffset *egopb.Offset, err error) {
	pool, err := s.activePool()
	if err != nil {
		return nil, err
	}

	statement := s.sb.
		Select(columns...).
		From(tableName).
		Where(sq.Eq{"projection_name": projectionID.GetProjectionName()}).
		Where(sq.Eq{"shard_number": projectionID.GetShardNumber()})

	query, args, err := statement.ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build the select sql statement: %w", err)
	}

	row := new(offsetRow)
	if err := selectOne(ctx, pool, row, query, args...); err != nil {
		return nil, fmt.Errorf("failed to fetch the current offset from the database: %w", err)
	}

	// no record found
	if row.ProjectionName == "" {
		return nil, nil
	}

	return &egopb.Offset{
		ShardNumber:    row.ShardNumber,
		ProjectionName: row.ProjectionName,
		Value:          row.CurrentOffset,
		Timestamp:      row.Timestamp,
	}, nil
}

// ResetOffset resets the offset of given projection to a given value across all shards
func (s *OffsetStore) ResetOffset(ctx context.Context, projectionName string, value int64) error {
	pool, err := s.activePool()
	if err != nil {
		return err
	}

	timestamp := time.Now().UnixMilli()

	statement := s.sb.
		Update(tableName).
		Set("current_offset", value).
		Set("timestamp", timestamp).
		Where(sq.Eq{"projection_name": projectionName})

	query, args, err := statement.ToSql()
	if err != nil {
		return fmt.Errorf("unable to build sql update statement: %w", err)
	}

	if _, err := pool.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("failed to reset offset: %w", err)
	}

	return nil
}
