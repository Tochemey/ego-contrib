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
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/georgysavva/scany/v2/sqlscan"
	"github.com/google/uuid"

	// register the pure Go SQLite driver, which needs no cgo
	_ "modernc.org/sqlite"
)

const (
	// driverName is the name modernc.org/sqlite registers with database/sql
	driverName = "sqlite"

	// pieces of the driver connection string
	dsnScheme        = "file:"
	pragmaParam      = "_pragma"
	txLockParam      = "_txlock"
	txLockImmediate  = "immediate"
	memoryModeParam  = "mode=memory"
	sharedCacheParam = "cache=shared"

	// pragmas the named configuration fields map to
	pragmaJournalMode = "journal_mode"
	pragmaSynchronous = "synchronous"
	pragmaBusyTimeout = "busy_timeout"

	// inMemoryNamePrefix prefixes the generated name of an in-memory database
	inMemoryNamePrefix = "ego-"
)

// Sqlite is the subset of *sql.DB the offset store relies on.
//
// A *sql.DB satisfies it as is. This lets the store run on a handle shared with the
// rest of the application, on a handle wrapped for instrumentation, or on a mock in
// unit tests. Pass an implementation to NewOffsetStoreWithSqlite.
//
// Close is deliberately not part of the interface: a handle given to the store stays
// owned by the caller and is never closed by the store.
type Sqlite interface {
	// ExecContext executes a statement that does not return rows
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	// QueryContext executes a statement that returns rows
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	// PingContext verifies the database is reachable
	PingContext(ctx context.Context) error
}

// enforce interface implementation
var _ Sqlite = (*sql.DB)(nil)

// openDB opens a database handle from the given configuration, applies the pool
// settings and verifies the database is reachable. The handle is closed when the
// verification fails.
func openDB(ctx context.Context, config *Config) (*sql.DB, error) {
	db, err := sql.Open(driverName, dataSourceName(config))
	if err != nil {
		return nil, fmt.Errorf("failed to open the database: %w", err)
	}

	db.SetMaxOpenConns(config.MaxOpenConnections)
	db.SetMaxIdleConns(config.MaxIdleConnections)
	db.SetConnMaxLifetime(config.ConnMaxLifetime)
	db.SetConnMaxIdleTime(config.ConnMaxIdleTime)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to ping the database: %w", err)
	}

	return db, nil
}

// dataSourceName builds the driver connection string from the configuration.
//
// Transactions are opened with BEGIN IMMEDIATE. A deferred transaction that takes
// the write lock only on its first write can be refused outright when another
// writer holds it, without waiting for the busy timeout; taking the lock upfront
// turns that case into a wait the timeout governs.
func dataSourceName(config *Config) string {
	pragmas := map[string]string{
		pragmaJournalMode: string(config.JournalMode),
		pragmaSynchronous: string(config.Synchronous),
		pragmaBusyTimeout: strconv.FormatInt(config.BusyTimeout.Milliseconds(), 10),
	}

	// caller supplied pragmas win over the ones derived from the named settings
	for name, value := range config.Pragmas {
		pragmas[name] = value
	}

	// sort the names so the same configuration always yields the same string
	names := make([]string, 0, len(pragmas))
	for name := range pragmas {
		names = append(names, name)
	}
	sort.Strings(names)

	params := make([]string, 0, len(names)+3)
	for _, name := range names {
		params = append(params, pragmaParam+"="+url.QueryEscape(name+"("+pragmas[name]+")"))
	}
	params = append(params, txLockParam+"="+txLockImmediate)

	path := config.DBPath
	if path == "" {
		// An anonymous in-memory database is private to a single connection, so a
		// pool would hand out a different empty database on every call. A named one
		// in shared cache mode is seen by every connection of this handle, and the
		// unique name keeps it separate from any other store in the process.
		path = inMemoryNamePrefix + uuid.NewString()
		params = append(params, memoryModeParam, sharedCacheParam)
	}

	return dsnScheme + path + "?" + strings.Join(params, "&")
}

// selectOne fetches a single row and scans it into dst.
// It returns nil when there is no matching row.
func selectOne(ctx context.Context, db Sqlite, dst any, query string, args ...any) error {
	if err := sqlscan.Get(ctx, db, dst, query, args...); err != nil {
		if sqlscan.NotFound(err) {
			return nil
		}
		return err
	}
	return nil
}

// selectAll fetches every matching row and scans them into dst.
// It returns nil when there is no matching row.
func selectAll(ctx context.Context, db Sqlite, dst any, query string, args ...any) error {
	if err := sqlscan.Select(ctx, db, dst, query, args...); err != nil {
		if sqlscan.NotFound(err) {
			return nil
		}
		return err
	}
	return nil
}
