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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Pool is the subset of *pgxpool.Pool the durable store relies on.
//
// A *pgxpool.Pool satisfies it as is, and so does pgxmock.PgxPoolIface. This lets the
// store run on a pool shared with the rest of the application, on a pool wrapped for
// instrumentation, or on a mock in unit tests. Pass an implementation to
// NewDurableStoreWithPool.
//
// Close is deliberately not part of the interface: a pool handed to the store stays
// owned by the caller and is never closed by the store.
type Pool interface {
	// Exec executes an SQL statement that does not return rows
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	// Query executes an SQL statement that returns rows
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	// Ping verifies the database is reachable
	Ping(ctx context.Context) error
}

// enforce interface implementation
var _ Pool = (*pgxpool.Pool)(nil)

// newPool builds a connection pool from the given configuration and verifies
// the database is reachable before handing it back. The pool is closed when
// the verification fails.
func newPool(ctx context.Context, config *Config) (*pgxpool.Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(connectionString(config))
	if err != nil {
		return nil, fmt.Errorf("failed to parse connection string: %w", err)
	}

	poolConfig.MaxConns = int32(config.MaxConnections)
	poolConfig.MinConns = int32(config.MinConnections)
	poolConfig.MaxConnLifetime = config.MaxConnectionLifetime
	poolConfig.MaxConnIdleTime = config.MaxConnIdleTime
	poolConfig.HealthCheckPeriod = config.HealthCheckPeriod

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create the connection pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping the database connection: %w", err)
	}

	return pool, nil
}

// connectionString builds the database connection string from the configuration
func connectionString(config *Config) string {
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

// selectOne fetches a single row and scans it into dst.
// It returns nil when there is no matching row.
func selectOne(ctx context.Context, pool Pool, dst any, query string, args ...any) error {
	if err := pgxscan.Get(ctx, pool, dst, query, args...); err != nil {
		if pgxscan.NotFound(err) {
			return nil
		}
		return err
	}
	return nil
}

// selectAll fetches every matching row and scans them into dst.
// It returns nil when there is no matching row.
func selectAll(ctx context.Context, pool Pool, dst any, query string, args ...any) error {
	if err := pgxscan.Select(ctx, pool, dst, query, args...); err != nil {
		if pgxscan.NotFound(err) {
			return nil
		}
		return err
	}
	return nil
}
