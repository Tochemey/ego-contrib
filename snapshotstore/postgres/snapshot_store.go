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
	"github.com/tochemey/ego/v4/egopb"
	"github.com/tochemey/ego/v4/persistence"
	"google.golang.org/protobuf/proto"
)

var (
	columns = []string{
		"persistence_id",
		"sequence_number",
		"state_payload",
		"state_manifest",
		"timestamp",
		"encryption_key_id",
		"is_encrypted",
	}

	tableName = "snapshots_store"
)

// SnapshotStore implements the persistence.SnapshotStore interface
// and helps persist entity snapshots in a postgres database
type SnapshotStore struct {
	db database
	sb sq.StatementBuilderType
	// guards connection state transitions
	mu        sync.Mutex
	connected bool
}

// enforce interface implementation
var _ persistence.SnapshotStore = (*SnapshotStore)(nil)

// NewSnapshotStore creates a new instance of SnapshotStore
func NewSnapshotStore(config *Config) *SnapshotStore {
	// create the underlying db connection
	db := newDatabase(newConfig(config))
	return &SnapshotStore{
		db: db,
		sb: sq.StatementBuilder.PlaceholderFormat(sq.Dollar),
	}
}

// Connect connects to the underlying postgres database
func (s *SnapshotStore) Connect(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.connected {
		return nil
	}

	if err := s.db.Connect(ctx); err != nil {
		return err
	}

	s.connected = true
	return nil
}

// Disconnect disconnects from the underlying postgres database
func (s *SnapshotStore) Disconnect(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.connected {
		return nil
	}

	if err := s.db.Disconnect(ctx); err != nil {
		return err
	}

	s.connected = false
	return nil
}

// Ping verifies a connection to the database is still alive, establishing a connection if necessary.
func (s *SnapshotStore) Ping(ctx context.Context) error {
	s.mu.Lock()
	if !s.connected {
		s.mu.Unlock()
		return s.Connect(ctx)
	}
	s.mu.Unlock()

	return s.db.Ping(ctx)
}

// WriteSnapshot persists a snapshot for a given persistenceID.
func (s *SnapshotStore) WriteSnapshot(ctx context.Context, snapshot *egopb.Snapshot) error {
	if !s.isConnected() {
		return errors.New("snapshot store is not connected")
	}

	if snapshot == nil || proto.Equal(snapshot, &egopb.Snapshot{}) {
		return nil
	}

	bytea, _ := proto.Marshal(snapshot.GetState())
	manifest := string(snapshot.GetState().ProtoReflect().Descriptor().FullName())

	statement := s.sb.
		Insert(tableName).
		Columns(columns...).
		Values(
			snapshot.GetPersistenceId(),
			snapshot.GetSequenceNumber(),
			bytea,
			manifest,
			snapshot.GetTimestamp(),
			snapshot.GetEncryptionKeyId(),
			snapshot.GetIsEncrypted(),
		).Suffix("ON CONFLICT (persistence_id, sequence_number) " +
		"DO UPDATE SET " +
		"state_payload = EXCLUDED.state_payload, " +
		"state_manifest = EXCLUDED.state_manifest, " +
		"timestamp = EXCLUDED.timestamp, " +
		"encryption_key_id = EXCLUDED.encryption_key_id, " +
		"is_encrypted = EXCLUDED.is_encrypted",
	)

	query, args, err := statement.ToSql()
	if err != nil {
		return fmt.Errorf("unable to build sql upsert statement: %w", err)
	}

	if _, err = s.db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("failed to write snapshot: %w", err)
	}

	return nil
}

// GetLatestSnapshot fetches the latest snapshot for a given persistenceID.
// Returns nil when no snapshot is found.
func (s *SnapshotStore) GetLatestSnapshot(ctx context.Context, persistenceID string) (*egopb.Snapshot, error) {
	if !s.isConnected() {
		return nil, errors.New("snapshot store is not connected")
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

	row := new(snapshotRow)
	err = s.db.Select(ctx, row, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch the latest snapshot from the database: %w", err)
	}

	// no record found
	if row.PersistenceID == "" {
		return nil, nil
	}

	return row.ToSnapshot()
}

// DeleteSnapshots deletes all snapshots for a given persistenceID up to a given sequence number (inclusive).
func (s *SnapshotStore) DeleteSnapshots(ctx context.Context, persistenceID string, toSequenceNumber uint64) error {
	if !s.isConnected() {
		return errors.New("snapshot store is not connected")
	}

	statement := s.sb.
		Delete(tableName).
		Where(sq.Eq{"persistence_id": persistenceID}).
		Where(sq.LtOrEq{"sequence_number": toSequenceNumber})

	query, args, err := statement.ToSql()
	if err != nil {
		return fmt.Errorf("unable to build sql delete statement: %w", err)
	}

	if _, err = s.db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("failed to delete snapshots: %w", err)
	}

	return nil
}

// isConnected returns whether the store is currently connected
func (s *SnapshotStore) isConnected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connected
}
