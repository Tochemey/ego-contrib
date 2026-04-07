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
	"fmt"

	"github.com/georgysavva/scany/v2/pgxscan"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type database interface {
	// Connect connects to the underlying database
	Connect(ctx context.Context) error
	// Disconnect closes the underlying opened underlying connection database
	Disconnect(ctx context.Context) error
	// Ping verifies a connection to the database is still alive
	Ping(ctx context.Context) error
	// Select fetches a single row from the database and automatically scanned it into the dst.
	// It returns an error in case of failure. When there is no record no errors is return.
	Select(ctx context.Context, dst any, query string, args ...any) error
	// SelectAll fetches a set of rows as defined by the query and scanned those record in the dst.
	// It returns nil when there is no records to fetch.
	SelectAll(ctx context.Context, dst any, query string, args ...any) error
	// Exec executes an SQL statement against the database and returns the appropriate result or an error.
	Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error)
}

// postgres helps interact with the database
type postgres struct {
	connStr string
	pool    *pgxpool.Pool
	config  *dbConfig
}

var _ database = (*postgres)(nil)

// newDatabase returns a store connecting to the given database
func newDatabase(config *dbConfig) database {
	pg := new(postgres)
	pg.config = config
	pg.connStr = createConnectionString(config)
	return pg
}

// Connect will connect to our database
func (pg *postgres) Connect(ctx context.Context) error {
	// create the connection config
	config, err := pgxpool.ParseConfig(pg.connStr)
	if err != nil {
		return fmt.Errorf("failed to parse connection string: %w", err)
	}

	// amend some of the configuration
	config.MaxConns = int32(pg.config.MaxConnections)
	config.MaxConnLifetime = pg.config.MaxConnectionLifetime
	config.MaxConnIdleTime = pg.config.MaxConnIdleTime
	config.MinConns = int32(pg.config.MinConnections)
	config.HealthCheckPeriod = pg.config.HealthCheckPeriod

	// connect to the pool
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return fmt.Errorf("failed to create the connection pool: %w", err)
	}

	// let us test the connection
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("failed to ping the database connection: %w", err)
	}

	// set the db handle
	pg.pool = pool
	return nil
}

// createConnectionString will create the database connection string from the
// supplied connection details
func createConnectionString(config *dbConfig) string {
	info := fmt.Sprintf("host=%s port=%d user=%s dbname=%s sslmode=%s",
		config.DBHost, config.DBPort, config.DBUser, config.DBName, config.DBSSLMode)
	// The database driver gets confused in cases where the user has no password
	// set but a password is passed, so only set password if its non-empty
	if config.DBPassword != "" {
		info += fmt.Sprintf(" password=%s", config.DBPassword)
	}

	if config.DBSchema != "" {
		info += fmt.Sprintf(" search_path=%s", config.DBSchema)
	}

	return info
}

// Ping verifies the database connection is still alive
func (pg *postgres) Ping(ctx context.Context) error {
	return pg.pool.Ping(ctx)
}

// Exec executes a sql query without returning rows against the database
func (pg *postgres) Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	return pg.pool.Exec(ctx, query, args...)
}

// SelectAll fetches rows
func (pg *postgres) SelectAll(ctx context.Context, dst any, query string, args ...any) error {
	err := pgxscan.Select(ctx, pg.pool, dst, query, args...)
	if err != nil {
		if pgxscan.NotFound(err) {
			return nil
		}

		return err
	}
	return nil
}

// Select fetches only one row
func (pg *postgres) Select(ctx context.Context, dst any, query string, args ...any) error {
	err := pgxscan.Get(ctx, pg.pool, dst, query, args...)
	if err != nil {
		if pgxscan.NotFound(err) {
			return nil
		}
		return err
	}

	return nil
}

// Disconnect the database connection.
// nolint
func (pg *postgres) Disconnect(_ context.Context) error {
	if pg.pool == nil {
		return nil
	}
	pg.pool.Close()
	return nil
}
