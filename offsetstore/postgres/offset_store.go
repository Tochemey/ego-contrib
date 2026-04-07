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
	"time"

	sq "github.com/Masterminds/squirrel"
	"go.uber.org/atomic"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/ego/v4/egopb"
	"github.com/tochemey/ego/v4/offsetstore"

	postgres "github.com/tochemey/ego-contrib/offsetstore/postgres/internal"
)

var (
	columns = []string{
		"projection_name",
		"shard_number",
		"current_offset",
		"timestamp",
	}

	tableName = "offsets_store"
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

// OffsetStore implements the OffsetStore interface
// and helps persist offsets in a postgres database
type OffsetStore struct {
	db postgres.Postgres
	sb sq.StatementBuilderType
	// hold the connection state to avoid multiple connection of the same instance
	connected *atomic.Bool
}

// ensure the complete implementation of the OffsetStore interface
var _ offsetstore.OffsetStore = (*OffsetStore)(nil)

// NewOffsetStore creates an instance of OffsetStore
func NewOffsetStore(config *Config) *OffsetStore {
	// create the underlying db connection
	dbConfig := postgres.NewConfig(config.DBHost, config.DBPort, config.DBUser, config.DBPassword, config.DBName)
	dbConfig.DBSchema = config.DBSchema
	db := postgres.New(dbConfig)
	return &OffsetStore{
		db:        db,
		sb:        sq.StatementBuilder.PlaceholderFormat(sq.Dollar),
		connected: atomic.NewBool(false),
	}
}

// Connect connects to the underlying postgres database
func (x *OffsetStore) Connect(ctx context.Context) error {
	// check whether this instance of the journal is connected or not
	if x.connected.Load() {
		return nil
	}

	// connect to the underlying db
	if err := x.db.Connect(ctx); err != nil {
		return err
	}

	// set the connection status
	x.connected.Store(true)

	return nil
}

// Disconnect disconnects from the underlying postgres database
func (x *OffsetStore) Disconnect(ctx context.Context) error {
	// check whether this instance of the journal is connected or not
	if !x.connected.Load() {
		return nil
	}

	// disconnect the underlying database
	if err := x.db.Disconnect(ctx); err != nil {
		return err
	}
	// set the connection status
	x.connected.Store(false)

	return nil
}

// WriteOffset writes an offset into the offset store
func (x *OffsetStore) WriteOffset(ctx context.Context, offset *egopb.Offset) error {
	// check whether this instance of the offset store is connected or not
	if !x.connected.Load() {
		return errors.New("offset store is not connected")
	}

	// make sure the record is defined
	if offset == nil || proto.Equal(offset, new(egopb.Offset)) {
		return errors.New("offset record is not defined")
	}

	// create the upsert statement
	upsertBuilder := x.sb.
		Insert(tableName).
		Columns(columns...).
		Values(
			offset.GetProjectionName(),
			offset.GetShardNumber(),
			offset.GetValue(),
			offset.GetTimestamp()).
		Suffix("ON CONFLICT (projection_name, shard_number) " +
			"DO UPDATE SET current_offset = EXCLUDED.current_offset, timestamp = EXCLUDED.timestamp")

	// get the SQL statement to run
	query, args, err := upsertBuilder.ToSql()
	if err != nil {
		return fmt.Errorf("unable to build sql upsert statement: %w", err)
	}

	// execute the upsert
	_, err = x.db.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to write offset: %w", err)
	}

	return nil
}

// GetCurrentOffset returns the current offset of a given projection id
func (x *OffsetStore) GetCurrentOffset(ctx context.Context, projectionID *egopb.ProjectionId) (currentOffset *egopb.Offset, err error) {
	// check whether this instance of the offset store is connected or not
	if !x.connected.Load() {
		return nil, errors.New("offset store is not connected")
	}

	// create the SQL statement
	statement := x.sb.
		Select(columns...).
		From(tableName).
		Where(sq.Eq{"projection_name": projectionID.GetProjectionName()}).
		Where(sq.Eq{"shard_number": projectionID.GetShardNumber()})

	// get the sql statement and the arguments
	query, args, err := statement.ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build the select sql statement: %w", err)
	}

	row := new(offsetRow)
	err = x.db.Select(ctx, row, query, args...)
	if err != nil {
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
func (x *OffsetStore) ResetOffset(ctx context.Context, projectionName string, value int64) error {
	// check whether this instance of the offset store is connected or not
	if !x.connected.Load() {
		return errors.New("offset store is not connected")
	}

	// define the current timestamp
	timestamp := time.Now().UnixMilli()

	// create the sql statement
	statement := x.sb.
		Update(tableName).
		Set("current_offset", value).
		Set("timestamp", timestamp).
		Where(sq.Eq{"projection_name": projectionName})

	// get the SQL statement to run
	query, args, err := statement.ToSql()
	if err != nil {
		return fmt.Errorf("unable to build sql update statement: %w", err)
	}

	// execute the update
	_, err = x.db.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to reset offset: %w", err)
	}

	return nil
}

// Ping verifies a connection to the database is still alive, establishing a connection if necessary.
func (x *OffsetStore) Ping(ctx context.Context) error {
	// check whether we are connected or not
	if !x.connected.Load() {
		return x.Connect(ctx)
	}

	return x.db.Ping(ctx)
}
