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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tochemey/ego/v4/egopb"
	"github.com/tochemey/ego/v4/persistence"
	"google.golang.org/protobuf/proto"
)

var (
	columns = []string{
		"persistence_id",
		"sequence_number",
		"is_deleted",
		"event_payload",
		"event_manifest",
		"timestamp",
		"shard_number",
		"encryption_key_id",
		"is_encrypted",
	}

	tableName = "events_store"

	// errNotConnected is returned when an operation runs against a store that is not connected
	errNotConnected = errors.New("journal store is not connected")
)

// defaultInsertBatchSize is the number of events bulk-inserted per statement.
// This keeps every statement below the postgres 65535 bind parameter limit.
const defaultInsertBatchSize = 500

// EventsStore implements the persistence.EventsStore interface
// and helps persist events in a postgres database
type EventsStore struct {
	// config holds the settings used to build the pool.
	// It is only set when the store owns its pool.
	config *Config
	// pool runs the queries. It is either built from config in Connect
	// or supplied by the caller through NewEventsStoreWithPool.
	pool Pool
	// owned is the pool built by the store from config.
	// It is nil when the pool was supplied by the caller, in which case the store never closes it.
	owned *pgxpool.Pool
	sb    sq.StatementBuilderType
	// insertBatchSize represents the chunk of events to bulk insert per statement.
	insertBatchSize int
	// guards connection state transitions
	mu        sync.Mutex
	connected bool
}

// enforce interface implementation
var _ persistence.EventsStore = (*EventsStore)(nil)

// NewEventsStore creates an events store that builds and owns its own connection pool.
// The pool is opened by Connect and closed by Disconnect.
func NewEventsStore(config *Config) *EventsStore {
	return &EventsStore{
		config:          config.sanitize(),
		sb:              sq.StatementBuilder.PlaceholderFormat(sq.Dollar),
		insertBatchSize: defaultInsertBatchSize,
	}
}

// NewEventsStoreWithPool creates an events store backed by a pool supplied by the caller.
// The caller keeps ownership of the pool: Connect only verifies it is reachable and
// Disconnect never closes it, so the same pool can be shared with other stores
// and with the rest of the application.
func NewEventsStoreWithPool(pool Pool) *EventsStore {
	return &EventsStore{
		pool:            pool,
		sb:              sq.StatementBuilder.PlaceholderFormat(sq.Dollar),
		insertBatchSize: defaultInsertBatchSize,
	}
}

// Connect connects to the underlying postgres database.
// When the store owns its pool, the pool is created here. When the pool was supplied
// by the caller, Connect only verifies the database is reachable.
func (s *EventsStore) Connect(ctx context.Context) error {
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
		return errors.New("journal store pool is not defined")
	}

	if err := s.pool.Ping(ctx); err != nil {
		return fmt.Errorf("failed to ping the database connection: %w", err)
	}

	s.connected = true
	return nil
}

// Disconnect disconnects from the underlying postgres database.
// The pool is only closed when it was built by the store.
func (s *EventsStore) Disconnect(context.Context) error {
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
func (s *EventsStore) Ping(ctx context.Context) error {
	pool, err := s.activePool()
	if err != nil {
		return s.Connect(ctx)
	}
	return pool.Ping(ctx)
}

// activePool returns the pool to run queries against, or an error when the store is not connected
func (s *EventsStore) activePool() (Pool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.connected {
		return nil, errNotConnected
	}
	return s.pool, nil
}

// PersistenceIDs returns the distinct list of all the persistence ids in the journal store
func (s *EventsStore) PersistenceIDs(ctx context.Context, pageSize uint64, pageToken string) (persistenceIDs []string, nextPageToken string, err error) {
	pool, err := s.activePool()
	if err != nil {
		return nil, "", err
	}

	statement := s.sb.
		Select("persistence_id").
		Distinct().
		From(tableName).
		Limit(pageSize).
		OrderBy("persistence_id ASC")

	if pageToken != "" {
		statement = statement.Where(sq.Gt{"persistence_id": pageToken})
	}

	query, args, err := statement.ToSql()
	if err != nil {
		return nil, "", fmt.Errorf("failed to build the sql statement: %w", err)
	}

	type row struct {
		PersistenceID string
	}

	var rows []*row
	if err := selectAll(ctx, pool, &rows, query, args...); err != nil {
		return nil, "", fmt.Errorf("failed to fetch the events from the database: %w", err)
	}

	if len(rows) == 0 {
		return nil, "", nil
	}

	persistenceIDs = make([]string, len(rows))
	for index, row := range rows {
		persistenceIDs[index] = row.PersistenceID
	}

	nextPageToken = persistenceIDs[len(persistenceIDs)-1]
	return persistenceIDs, nextPageToken, nil
}

// WriteEvents writes a bunch of events into the underlying postgres database
func (s *EventsStore) WriteEvents(ctx context.Context, events []*egopb.Event) error {
	pool, err := s.activePool()
	if err != nil {
		return err
	}

	if len(events) == 0 {
		return nil
	}

	// begin a transaction to make sure the events are written atomically
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("failed to obtain a database transaction: %w", err)
	}

	if err := s.insertEvents(ctx, tx, events); err != nil {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
			return fmt.Errorf("unable to rollback db transaction: %w: %w", rollbackErr, err)
		}
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to record events: %w", err)
	}

	return nil
}

// insertEvents bulk-inserts the events in batches within the given transaction
func (s *EventsStore) insertEvents(ctx context.Context, tx pgx.Tx, events []*egopb.Event) error {
	statement := s.sb.Insert(tableName).Columns(columns...)
	for index, event := range events {
		eventBytes, err := proto.Marshal(event.GetEvent())
		if err != nil {
			return fmt.Errorf("failed to marshal event at index %d: %w", index, err)
		}

		eventManifest := string(event.GetEvent().ProtoReflect().Descriptor().FullName())

		statement = statement.Values(
			event.GetPersistenceId(),
			event.GetSequenceNumber(),
			event.GetIsDeleted(),
			eventBytes,
			eventManifest,
			event.GetTimestamp(),
			event.GetShard(),
			event.GetEncryptionKeyId(),
			event.GetIsEncrypted(),
		)

		if (index+1)%s.insertBatchSize == 0 || index == len(events)-1 {
			query, args, err := statement.ToSql()
			if err != nil {
				return fmt.Errorf("unable to build sql insert statement: %w", err)
			}

			if _, err := tx.Exec(ctx, query, args...); err != nil {
				return fmt.Errorf("failed to record events: %w", err)
			}

			// reset the statement for the next batch
			statement = s.sb.Insert(tableName).Columns(columns...)
		}
	}
	return nil
}

// DeleteEvents deletes events from the postgres up to a given sequence number (inclusive)
func (s *EventsStore) DeleteEvents(ctx context.Context, persistenceID string, toSequenceNumber uint64) error {
	pool, err := s.activePool()
	if err != nil {
		return err
	}

	statement := s.sb.
		Delete(tableName).
		Where(sq.Eq{"persistence_id": persistenceID}).
		Where(sq.LtOrEq{"sequence_number": toSequenceNumber})

	query, args, err := statement.ToSql()
	if err != nil {
		return fmt.Errorf("failed to build the delete events sql statement: %w", err)
	}

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("failed to obtain a database transaction: %w", err)
	}

	if _, execErr := tx.Exec(ctx, query, args...); execErr != nil {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
			return fmt.Errorf("unable to rollback db transaction: %w: %w", rollbackErr, execErr)
		}
		return fmt.Errorf("failed to delete events from the database: %w", execErr)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit delete events: %w", err)
	}

	return nil
}

// ReplayEvents fetches events for a given persistence ID from a given sequence number(inclusive) to a given sequence number(inclusive)
func (s *EventsStore) ReplayEvents(ctx context.Context, persistenceID string, fromSequenceNumber, toSequenceNumber uint64, limit uint64) ([]*egopb.Event, error) {
	pool, err := s.activePool()
	if err != nil {
		return nil, err
	}

	statement := s.sb.
		Select(columns...).
		From(tableName).
		Where(sq.Eq{"persistence_id": persistenceID}).
		Where(sq.GtOrEq{"sequence_number": fromSequenceNumber}).
		Where(sq.LtOrEq{"sequence_number": toSequenceNumber}).
		OrderBy("sequence_number ASC").
		Limit(limit)

	query, args, err := statement.ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build the select sql statement: %w", err)
	}

	var rows rows
	if err := selectAll(ctx, pool, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("failed to fetch the events from the database: %w", err)
	}

	return rows.ToEvents()
}

// GetLatestEvent fetches the latest event
func (s *EventsStore) GetLatestEvent(ctx context.Context, persistenceID string) (*egopb.Event, error) {
	pool, err := s.activePool()
	if err != nil {
		return nil, err
	}

	statement := s.sb.
		Select(columns...).
		From(tableName).
		Where(sq.Eq{"persistence_id": persistenceID}).
		OrderBy("sequence_number DESC").
		Limit(1)

	query, args, err := statement.ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build the select sql statement: %w", err)
	}

	row := new(row)
	if err := selectOne(ctx, pool, row, query, args...); err != nil {
		return nil, fmt.Errorf("failed to fetch the latest event from the database: %w", err)
	}

	if row.PersistenceID == "" {
		return nil, nil
	}

	return row.ToEvent()
}

// GetShardEvents returns the next (max) events after the offset in the journal for a given shard
func (s *EventsStore) GetShardEvents(ctx context.Context, shardNumber uint64, offset int64, limit uint64) ([]*egopb.Event, int64, error) {
	pool, err := s.activePool()
	if err != nil {
		return nil, 0, err
	}

	statement := s.sb.
		Select(columns...).
		From(tableName).
		Where(sq.Eq{"shard_number": shardNumber}).
		Where(sq.Gt{"timestamp": offset}).
		OrderBy("timestamp ASC").
		Limit(limit)

	query, args, err := statement.ToSql()
	if err != nil {
		return nil, 0, fmt.Errorf("failed to build the select sql statement: %w", err)
	}

	var rows rows
	if err := selectAll(ctx, pool, &rows, query, args...); err != nil {
		return nil, 0, fmt.Errorf("failed to fetch the events from the database: %w", err)
	}

	if len(rows) == 0 {
		return nil, 0, nil
	}

	events, err := rows.ToEvents()
	if err != nil {
		return nil, 0, err
	}

	nextOffset := events[len(events)-1].GetTimestamp()
	return events, nextOffset, nil
}

// ShardOffsets returns every distinct shard in the journal mapped to the
// offset (timestamp) of its most recent event. An empty journal yields an empty map.
func (s *EventsStore) ShardOffsets(ctx context.Context) (map[uint64]int64, error) {
	pool, err := s.activePool()
	if err != nil {
		return nil, err
	}

	statement := s.sb.
		Select("shard_number", "MAX(timestamp) AS current_offset").
		From(tableName).
		GroupBy("shard_number")

	query, args, err := statement.ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build the select sql statement: %w", err)
	}

	type row struct {
		ShardNumber   uint64
		CurrentOffset int64
	}

	var rows []*row
	if err := selectAll(ctx, pool, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("failed to fetch the shard offsets from the database: %w", err)
	}

	offsets := make(map[uint64]int64, len(rows))
	for _, row := range rows {
		offsets[row.ShardNumber] = row.CurrentOffset
	}

	return offsets, nil
}
