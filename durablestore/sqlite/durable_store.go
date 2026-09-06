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

	sq "github.com/Masterminds/squirrel"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/ego/v4/egopb"
	"github.com/tochemey/ego/v4/persistence"
)

// tableName is the table the durable states are written to
const tableName = "states_store"

// columns of tableName, in the order the insert statement binds them
var columns = []string{
	"persistence_id",
	"version_number",
	"state_payload",
	"state_manifest",
	"timestamp",
	"shard_number",
}

// errNotConnected is returned when an operation runs against a store that is not connected
var errNotConnected = errors.New("durable store is not connected")

// DurableStore implements the persistence.StateStore interface
// and helps persist durable states in a SQLite database
type DurableStore struct {
	// config holds the settings used to open the database handle.
	// It is only set when the store owns its handle.
	config *Config
	// db runs the queries. It is either opened from config in Connect
	// or supplied by the caller through NewDurableStoreWithSqlite.
	db Sqlite
	// owned is the handle opened by the store from config.
	// It is nil when the handle was supplied by the caller, in which case the store never closes it.
	owned *sql.DB
	sb    sq.StatementBuilderType
	// guards connection state transitions
	mu        sync.Mutex
	connected bool
}

// enforce interface implementation
var _ persistence.StateStore = (*DurableStore)(nil)

// NewDurableStore creates a durable store that opens and owns its own database handle.
// The handle is opened by Connect and closed by Disconnect.
func NewDurableStore(config *Config) *DurableStore {
	return &DurableStore{
		config: config.sanitize(),
		sb:     sq.StatementBuilder.PlaceholderFormat(sq.Question),
	}
}

// NewDurableStoreWithSqlite creates a durable store backed by a database handle supplied
// by the caller. The caller keeps ownership: Connect only verifies the handle is
// reachable and Disconnect never closes it, so the same handle can be shared with
// other stores and with the rest of the application.
//
// Open the handle with the pragmas the store relies on, in particular write-ahead
// logging and a busy timeout. See the module README for a ready connection string.
func NewDurableStoreWithSqlite(db Sqlite) *DurableStore {
	return &DurableStore{
		db: db,
		sb: sq.StatementBuilder.PlaceholderFormat(sq.Question),
	}
}

// Connect connects to the underlying SQLite database.
// When the store owns its handle, the handle is opened here. When the handle was
// supplied by the caller, Connect only verifies the database is reachable.
func (s *DurableStore) Connect(ctx context.Context) error {
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
		return errors.New("durable store database handle is not defined")
	}

	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("failed to ping the database: %w", err)
	}

	s.connected = true
	return nil
}

// Disconnect disconnects from the underlying SQLite database.
// The handle is only closed when it was opened by the store.
func (s *DurableStore) Disconnect(context.Context) error {
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
func (s *DurableStore) Ping(ctx context.Context) error {
	db, err := s.activeDB()
	if err != nil {
		return s.Connect(ctx)
	}
	return db.PingContext(ctx)
}

// activeDB returns the handle to run queries against, or an error when the store is not connected
func (s *DurableStore) activeDB() (Sqlite, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.connected {
		return nil, errNotConnected
	}
	return s.db, nil
}

// WriteState writes a durable state into the underlying SQLite database
func (s *DurableStore) WriteState(ctx context.Context, state *egopb.DurableState) error {
	db, err := s.activeDB()
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
		"version_number = excluded.version_number, " +
		"state_payload = excluded.state_payload, " +
		"state_manifest = excluded.state_manifest, " +
		"timestamp = excluded.timestamp, " +
		"shard_number = excluded.shard_number",
	)

	query, args, err := statement.ToSql()
	if err != nil {
		return fmt.Errorf("unable to build sql insert statement: %w", err)
	}

	if _, err := db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("failed to record durable state: %w", err)
	}

	return nil
}

// GetLatestState fetches the latest durable state of a persistenceID
func (s *DurableStore) GetLatestState(ctx context.Context, persistenceID string) (*egopb.DurableState, error) {
	db, err := s.activeDB()
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
	if err := selectOne(ctx, db, row, query, args...); err != nil {
		return nil, fmt.Errorf("failed to fetch the latest durable state from the database: %w", err)
	}

	if row.PersistenceID == "" {
		return nil, nil
	}

	return row.ToDurableState()
}
