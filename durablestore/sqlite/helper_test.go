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
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tochemey/ego/v4/egopb"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// schemaDDL creates the durable store table used by the tests
const schemaDDL = `
	CREATE TABLE IF NOT EXISTS states_store
	(
	    persistence_id  TEXT    PRIMARY KEY,
	    version_number  INTEGER NOT NULL,
	    state_payload   BLOB    NOT NULL,
	    state_manifest  TEXT    NOT NULL,
	    timestamp       INTEGER NOT NULL,
	    shard_number    INTEGER NOT NULL
	);
`

// testConfig returns a configuration pointing at a database file in a directory
// the testing package removes when the test ends
func testConfig(t *testing.T) *Config {
	t.Helper()
	return &Config{DBPath: filepath.Join(t.TempDir(), "states.db")}
}

// newTestDB opens a handle on a temporary database and creates the schema on it
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := openDB(context.Background(), testConfig(t).sanitize())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	createSchema(t, db)
	return db
}

// createSchema creates the durable store table
func createSchema(t *testing.T, db Sqlite) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), schemaDDL)
	require.NoError(t, err)
}

// dropSchema removes the durable store table
func dropSchema(t *testing.T, db Sqlite) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), "DROP TABLE IF EXISTS "+tableName)
	require.NoError(t, err)
}

// countRows returns the number of rows held by the durable store table
func countRows(t *testing.T, db Sqlite) int {
	t.Helper()
	var count int
	require.NoError(t, selectOne(context.Background(), db, &count, "SELECT COUNT(*) FROM "+tableName))
	return count
}

// newConnectedStore returns a store connected to a temporary database whose schema exists
func newConnectedStore(t *testing.T) *DurableStore {
	t.Helper()
	store := NewDurableStore(testConfig(t))
	require.NoError(t, store.Connect(context.Background()))
	t.Cleanup(func() { _ = store.Disconnect(context.Background()) })

	db, err := store.activeDB()
	require.NoError(t, err)
	createSchema(t, db)
	return store
}

// newTestState creates a durable state for tests
func newTestState(t *testing.T, persistenceID string, version uint64, value string) *egopb.DurableState {
	t.Helper()
	state, err := anypb.New(wrapperspb.String(value))
	require.NoError(t, err)
	return &egopb.DurableState{
		PersistenceId:  persistenceID,
		VersionNumber:  version,
		ResultingState: state,
		Timestamp:      time.Now().UnixMilli(),
		Shard:          1,
	}
}
