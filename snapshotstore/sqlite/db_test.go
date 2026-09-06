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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigSanitize(t *testing.T) {
	t.Run("defaults are applied without touching the original", func(t *testing.T) {
		original := &Config{DBPath: "/tmp/snapshots.db"}
		sanitized := original.sanitize()

		assert.Equal(t, JournalModeWAL, sanitized.JournalMode)
		assert.Equal(t, SynchronousNormal, sanitized.Synchronous)
		assert.Equal(t, 5*time.Second, sanitized.BusyTimeout)
		assert.Equal(t, 4, sanitized.MaxOpenConnections)
		assert.Equal(t, 4, sanitized.MaxIdleConnections)
		assert.Equal(t, time.Hour, sanitized.ConnMaxLifetime)
		assert.Equal(t, 30*time.Minute, sanitized.ConnMaxIdleTime)

		assert.Empty(t, original.JournalMode)
		assert.Zero(t, original.MaxOpenConnections)
	})

	t.Run("explicit values are preserved", func(t *testing.T) {
		original := &Config{
			DBPath:             "/tmp/snapshots.db",
			JournalMode:        JournalModeDelete,
			Synchronous:        SynchronousFull,
			BusyTimeout:        time.Second,
			MaxOpenConnections: 1,
			MaxIdleConnections: 1,
			ConnMaxLifetime:    2 * time.Hour,
			ConnMaxIdleTime:    time.Hour,
		}
		assert.Equal(t, original, original.sanitize())
	})

	t.Run("idle connections follow the open ones", func(t *testing.T) {
		sanitized := (&Config{MaxOpenConnections: 7}).sanitize()
		assert.Equal(t, 7, sanitized.MaxIdleConnections)
	})

	t.Run("pragmas are copied", func(t *testing.T) {
		original := &Config{Pragmas: map[string]string{"cache_size": "-2000"}}
		sanitized := original.sanitize()

		original.Pragmas["cache_size"] = "-9999"
		assert.Equal(t, "-2000", sanitized.Pragmas["cache_size"])
	})
}

func TestDataSourceName(t *testing.T) {
	t.Run("a file database carries the pragmas and the immediate lock", func(t *testing.T) {
		dsn := dataSourceName((&Config{DBPath: "/var/lib/ego/snapshots.db"}).sanitize())

		assert.True(t, strings.HasPrefix(dsn, "file:/var/lib/ego/snapshots.db?"))
		assert.Contains(t, dsn, "_pragma=journal_mode%28WAL%29")
		assert.Contains(t, dsn, "_pragma=synchronous%28NORMAL%29")
		assert.Contains(t, dsn, "_pragma=busy_timeout%285000%29")
		assert.Contains(t, dsn, "_txlock=immediate")
		assert.NotContains(t, dsn, "mode=memory")
	})

	t.Run("an empty path yields a uniquely named shared in-memory database", func(t *testing.T) {
		first := dataSourceName((&Config{}).sanitize())
		second := dataSourceName((&Config{}).sanitize())

		assert.Contains(t, first, "mode=memory")
		assert.Contains(t, first, "cache=shared")
		assert.NotEqual(t, first, second)
	})

	t.Run("custom pragmas are added", func(t *testing.T) {
		dsn := dataSourceName((&Config{Pragmas: map[string]string{"cache_size": "-64000"}}).sanitize())
		assert.Contains(t, dsn, "_pragma=cache_size%28-64000%29")
	})

	t.Run("custom pragmas override the named settings", func(t *testing.T) {
		dsn := dataSourceName((&Config{
			JournalMode: JournalModeWAL,
			Pragmas:     map[string]string{"journal_mode": string(JournalModeMemory)},
		}).sanitize())

		assert.Contains(t, dsn, "_pragma=journal_mode%28MEMORY%29")
		assert.NotContains(t, dsn, "_pragma=journal_mode%28WAL%29")
	})

	t.Run("the same configuration always yields the same string", func(t *testing.T) {
		config := (&Config{DBPath: "/tmp/snapshots.db", Pragmas: map[string]string{"a": "1", "b": "2", "c": "3"}}).sanitize()
		assert.Equal(t, dataSourceName(config), dataSourceName(config))
	})
}

func TestOpenDB(t *testing.T) {
	ctx := context.Background()

	t.Run("the configured pragmas are applied to the connection", func(t *testing.T) {
		db, err := openDB(ctx, testConfig(t).sanitize())
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		var journalMode string
		require.NoError(t, db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode))
		assert.Equal(t, "wal", journalMode)

		var busyTimeout int
		require.NoError(t, db.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout))
		assert.Equal(t, 5000, busyTimeout)
	})

	t.Run("a custom pragma reaches the connection", func(t *testing.T) {
		config := testConfig(t)
		config.JournalMode = JournalModeTruncate
		db, err := openDB(ctx, config.sanitize())
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		var journalMode string
		require.NoError(t, db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode))
		assert.Equal(t, "truncate", journalMode)
	})

	t.Run("the pool settings are applied", func(t *testing.T) {
		config := testConfig(t)
		config.MaxOpenConnections = 2
		db, err := openDB(ctx, config.sanitize())
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		assert.Equal(t, 2, db.Stats().MaxOpenConnections)
	})

	t.Run("an unwritable path fails", func(t *testing.T) {
		config := &Config{DBPath: filepath.Join(t.TempDir(), "missing-directory", "snapshots.db")}
		db, err := openDB(ctx, config.sanitize())
		require.Error(t, err)
		assert.Nil(t, db)
	})

	t.Run("an in-memory database is shared by every connection of the handle", func(t *testing.T) {
		db, err := openDB(ctx, (&Config{}).sanitize())
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		_, err = db.ExecContext(ctx, "CREATE TABLE t(a TEXT)")
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, "INSERT INTO t VALUES('x')")
		require.NoError(t, err)

		// hold several connections at once so the read cannot reuse the writer
		var wg sync.WaitGroup
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				var count int
				assert.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM t").Scan(&count))
				assert.Equal(t, 1, count)
			}()
		}
		wg.Wait()
	})
}

func TestSelectHelpers(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	t.Run("selectOne returns nil when there is no row", func(t *testing.T) {
		var row snapshotRow
		require.NoError(t, selectOne(ctx, db, &row, "SELECT * FROM "+tableName+" WHERE persistence_id = ?", "missing"))
		assert.Empty(t, row.PersistenceID)
	})

	t.Run("selectAll returns nil when there is no row", func(t *testing.T) {
		var rows []*snapshotRow
		require.NoError(t, selectAll(ctx, db, &rows, "SELECT * FROM "+tableName))
		assert.Empty(t, rows)
	})

	t.Run("selectOne reports a broken statement", func(t *testing.T) {
		var row snapshotRow
		assert.Error(t, selectOne(ctx, db, &row, "NOT SQL"))
	})

	t.Run("selectAll reports a broken statement", func(t *testing.T) {
		var rows []*snapshotRow
		assert.Error(t, selectAll(ctx, db, &rows, "NOT SQL"))
	})
}

func TestConcurrentWriters(t *testing.T) {
	ctx := context.Background()
	store := newConnectedStore(t)

	const writers, perWriter = 8, 25

	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for w := range writers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := range perWriter {
				snapshot := newTestSnapshot(t, "persistence-"+string(rune('a'+w)), uint64(i+1), "state")
				if err := store.WriteSnapshot(ctx, snapshot); err != nil {
					errs <- err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err, "concurrent writers must not hit a locked database")
	}

	db, err := store.activeDB()
	require.NoError(t, err)
	assert.Equal(t, writers*perWriter, countRows(t, db))
}

// enforce that a *sql.DB is usable as the store handle
var _ Sqlite = (*sql.DB)(nil)
