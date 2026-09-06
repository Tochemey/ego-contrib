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

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	sq "github.com/Masterminds/squirrel"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/ego/v4/egopb"
	"github.com/tochemey/ego/v4/offsetstore"
)

// tableName is the table the offsets are written to
const tableName = "offsets_store"

// columns of tableName, in the order the insert statement binds them
var columns = []string{
	"projection_name",
	"shard_number",
	"current_offset",
	"timestamp",
}

// errNotConnected is returned when an operation runs against a store that is not connected
var errNotConnected = errors.New("offset store is not connected")

// offsetRow represents the offset entry in the offset store
type offsetRow struct {
	ProjectionName string
	ShardNumber    uint64
	CurrentOffset  int64
	Timestamp      int64
}

// OffsetStore implements the offsetstore.OffsetStore interface
// and helps persist offsets in a SQLite database
type OffsetStore struct {
	// config holds the settings used to open the database handle.
	// It is only set when the store owns its handle.
	config *Config
	// db runs the queries. It is either opened from config in Connect
	// or supplied by the caller through NewOffsetStoreWithSqlite.
	db Sqlite
	// owned is the handle opened by the store from config.
	// It is nil when the handle was supplied by the caller, in which case the store never closes it.
	owned *sql.DB
	sb    sq.StatementBuilderType
	// guards connection state transitions
	mu        sync.Mutex
	connected bool
}

// ensure the complete implementation of the OffsetStore interface
var _ offsetstore.OffsetStore = (*OffsetStore)(nil)

// NewOffsetStore creates an offset store that opens and owns its own database handle.
// The handle is opened by Connect and closed by Disconnect.
func NewOffsetStore(config *Config) *OffsetStore {
	return &OffsetStore{
		config: config.sanitize(),
		sb:     sq.StatementBuilder.PlaceholderFormat(sq.Question),
	}
}

// NewOffsetStoreWithSqlite creates an offset store backed by a database handle supplied
// by the caller. The caller keeps ownership: Connect only verifies the handle is
// reachable and Disconnect never closes it, so the same handle can be shared with
// other stores and with the rest of the application.
//
// Open the handle with the pragmas the store relies on, in particular write-ahead
// logging and a busy timeout. See the module README for a ready connection string.
func NewOffsetStoreWithSqlite(db Sqlite) *OffsetStore {
	return &OffsetStore{
		db: db,
		sb: sq.StatementBuilder.PlaceholderFormat(sq.Question),
	}
}

// Connect connects to the underlying SQLite database.
// When the store owns its handle, the handle is opened here. When the handle was
// supplied by the caller, Connect only verifies the database is reachable.
func (s *OffsetStore) Connect(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.connected {
		return nil
	}

	if s.config != nil {
		db, err := openDB(ctx, s.config)
		if err != nil {
			return err
		}
		s.owned = db
		s.db = db
		s.connected = true
		return nil
	}

	if s.db == nil {
		return errors.New("offset store database handle is not defined")
	}

	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("failed to ping the database: %w", err)
	}

	s.connected = true
	return nil
}

// Disconnect disconnects from the underlying SQLite database.
// The handle is only closed when it was opened by the store.
func (s *OffsetStore) Disconnect(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.connected {
		return nil
	}

	if s.owned != nil {
		if err := s.owned.Close(); err != nil {
			return fmt.Errorf("failed to close the database: %w", err)
		}
		s.owned = nil
		s.db = nil
	}

	s.connected = false
	return nil
}

// Ping verifies a connection to the database is still alive, establishing a connection if necessary.
func (s *OffsetStore) Ping(ctx context.Context) error {
	db, err := s.activeDB()
	if err != nil {
		return s.Connect(ctx)
	}
	return db.PingContext(ctx)
}

// activeDB returns the handle to run queries against, or an error when the store is not connected
func (s *OffsetStore) activeDB() (Sqlite, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.connected {
		return nil, errNotConnected
	}
	return s.db, nil
}

// WriteOffset writes an offset into the offset store
func (s *OffsetStore) WriteOffset(ctx context.Context, offset *egopb.Offset) error {
	db, err := s.activeDB()
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
			"DO UPDATE SET current_offset = excluded.current_offset, timestamp = excluded.timestamp")

	query, args, err := statement.ToSql()
	if err != nil {
		return fmt.Errorf("unable to build sql upsert statement: %w", err)
	}

	if _, err := db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("failed to write offset: %w", err)
	}

	return nil
}

// GetCurrentOffset returns the current offset of a given projection id
func (s *OffsetStore) GetCurrentOffset(ctx context.Context, projectionID *egopb.ProjectionId) (currentOffset *egopb.Offset, err error) {
	db, err := s.activeDB()
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
	if err := selectOne(ctx, db, row, query, args...); err != nil {
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
	db, err := s.activeDB()
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

	if _, err := db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("failed to reset offset: %w", err)
	}

	return nil
}
