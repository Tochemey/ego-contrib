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
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

func TestConnectionString(t *testing.T) {
	t.Run("with password and schema", func(t *testing.T) {
		config := &Config{
			DBHost:     "localhost",
			DBPort:     5432,
			DBName:     "ego",
			DBUser:     "ego",
			DBPassword: "secret",
			DBSchema:   "app",
			DBSSLMode:  "require",
		}
		connStr := connectionString(config)
		assert.Equal(t, "host=localhost port=5432 user=ego dbname=ego sslmode=require password=secret search_path=app", connStr)
	})
	t.Run("without password nor schema", func(t *testing.T) {
		config := &Config{
			DBHost:    "localhost",
			DBPort:    5432,
			DBName:    "ego",
			DBUser:    "ego",
			DBSSLMode: "disable",
		}
		connStr := connectionString(config)
		assert.Equal(t, "host=localhost port=5432 user=ego dbname=ego sslmode=disable", connStr)
	})
}

func TestConfigSanitize(t *testing.T) {
	t.Run("defaults are applied without touching the original", func(t *testing.T) {
		original := &Config{DBHost: "localhost"}
		sanitized := original.sanitize()

		assert.Equal(t, "disable", sanitized.DBSSLMode)
		assert.Equal(t, 4, sanitized.MaxConnections)
		assert.Equal(t, 0, sanitized.MinConnections)
		assert.Equal(t, time.Hour, sanitized.MaxConnectionLifetime)
		assert.Equal(t, 30*time.Minute, sanitized.MaxConnIdleTime)
		assert.Equal(t, time.Minute, sanitized.HealthCheckPeriod)

		assert.Empty(t, original.DBSSLMode)
		assert.Zero(t, original.MaxConnections)
		assert.Zero(t, original.HealthCheckPeriod)
	})
	t.Run("explicit values are preserved", func(t *testing.T) {
		original := &Config{
			DBSSLMode:             "verify-full",
			MaxConnections:        10,
			MinConnections:        2,
			MaxConnectionLifetime: 2 * time.Hour,
			MaxConnIdleTime:       time.Hour,
			HealthCheckPeriod:     5 * time.Minute,
		}
		sanitized := original.sanitize()
		assert.Equal(t, original, sanitized)
	})
}

// account is a test struct
type account struct {
	AccountID   string
	AccountName string
}

// PoolTestSuite will run the pool tests against the test container
type PoolTestSuite struct {
	suite.Suite
}

// In order for 'go test' to run this suite, we need to create
// a normal test function and pass our suite to suite.Run
func TestPoolTestSuite(t *testing.T) {
	suite.Run(t, new(PoolTestSuite))
}

func (s *PoolTestSuite) TestNewPool() {
	ctx := context.TODO()

	s.Run("with valid connection settings", func() {
		pool, err := newPool(ctx, testConfig().sanitize())
		s.Require().NoError(err)
		s.Require().NotNil(pool)
		pool.Close()
	})

	s.Run("with invalid database port", func() {
		config := testConfig()
		config.DBPort = -2
		pool, err := newPool(ctx, config.sanitize())
		s.Assert().Error(err)
		s.Assert().Nil(pool)
	})

	s.Run("with invalid database name", func() {
		config := testConfig()
		config.DBName = "wrong-name"
		pool, err := newPool(ctx, config.sanitize())
		s.Assert().Error(err)
		s.Assert().Nil(pool)
	})

	s.Run("with invalid database user", func() {
		config := testConfig()
		config.DBUser = "test-user"
		pool, err := newPool(ctx, config.sanitize())
		s.Assert().Error(err)
		s.Assert().Nil(pool)
	})

	s.Run("with invalid database password", func() {
		config := testConfig()
		config.DBPassword = "invalid-db-pass"
		pool, err := newPool(ctx, config.sanitize())
		s.Assert().Error(err)
		s.Assert().Nil(pool)
	})
}

func (s *PoolTestSuite) TestExec() {
	ctx := context.TODO()
	db, err := dbHandle(ctx)
	s.Require().NoError(err)
	defer func() { _ = db.Disconnect(ctx) }()

	s.Run("with valid SQL statement", func() {
		s.Require().NoError(db.DropTable(ctx, "accounts"))
		const schemaDDL = `
		CREATE TABLE accounts
		(
		    account_id		UUID,
			account_name 	VARCHAR(255)  NOT NULL,
		    PRIMARY KEY (account_id)
		);
	`
		_, err = db.Exec(ctx, schemaDDL)
		s.Assert().NoError(err)
	})

	s.Run("with invalid SQL statement", func() {
		const schemaDDL = `SOME-INVALID-SQL`
		_, err = db.Exec(ctx, schemaDDL)
		s.Assert().Error(err)
	})
}

func (s *PoolTestSuite) TestSelectOne() {
	ctx := context.TODO()
	db, err := dbHandle(ctx)
	s.Require().NoError(err)
	defer func() { _ = db.Disconnect(ctx) }()

	const selectSQL = `SELECT account_id, account_name FROM accounts WHERE account_id = $1`

	s.Run("with valid record", func() {
		s.Require().NoError(db.DropTable(ctx, "accounts"))
		s.Require().NoError(createTable(ctx, db))

		inserted := &account{
			AccountID:   uuid.New().String(),
			AccountName: "some-account",
		}
		s.Require().NoError(insertInto(ctx, db, inserted))

		selected := &account{}
		err = db.Select(ctx, selected, selectSQL, inserted.AccountID)
		s.Assert().NoError(err)
		s.Assert().Equal(inserted.AccountID, selected.AccountID)
		s.Assert().Equal(inserted.AccountName, selected.AccountName)
	})

	s.Run("with no records", func() {
		s.Require().NoError(db.DropTable(ctx, "accounts"))
		s.Require().NoError(createTable(ctx, db))

		var selected *account
		err = db.Select(ctx, selected, selectSQL, uuid.New().String())
		s.Assert().NoError(err)
		s.Assert().Nil(selected)
	})

	s.Run("with invalid SQL statement", func() {
		var selected *account
		err = db.Select(ctx, selected, "weird-sql", uuid.New().String())
		s.Assert().Error(err)
		s.Assert().Nil(selected)
	})
}

func (s *PoolTestSuite) TestSelectAll() {
	ctx := context.TODO()
	db, err := dbHandle(ctx)
	s.Require().NoError(err)
	defer func() { _ = db.Disconnect(ctx) }()

	const selectSQL = `SELECT account_id, account_name FROM accounts;`

	s.Run("with valid records", func() {
		s.Require().NoError(db.DropTable(ctx, "accounts"))
		s.Require().NoError(createTable(ctx, db))

		inserted := &account{
			AccountID:   uuid.New().String(),
			AccountName: "some-account",
		}
		s.Require().NoError(insertInto(ctx, db, inserted))

		var selected []*account
		err = db.SelectAll(ctx, &selected, selectSQL)
		s.Assert().NoError(err)
		s.Assert().Len(selected, 1)
	})

	s.Run("with no records", func() {
		s.Require().NoError(db.DropTable(ctx, "accounts"))
		s.Require().NoError(createTable(ctx, db))

		var selected []*account
		err = db.SelectAll(ctx, &selected, selectSQL)
		s.Assert().NoError(err)
		s.Assert().Nil(selected)
	})

	s.Run("with invalid SQL statement", func() {
		var selected []*account
		err = db.SelectAll(ctx, selected, "weird-sql", uuid.New().String())
		s.Assert().Error(err)
		s.Assert().Nil(selected)
	})
}

func (s *PoolTestSuite) TestDisconnect() {
	ctx := context.TODO()
	db, err := dbHandle(ctx)
	s.Require().NoError(err)

	// close the db connection
	err = db.Disconnect(ctx)
	s.Assert().NoError(err)

	// let us execute a query against a closed connection
	err = db.TableExists(ctx, "accounts")
	s.Assert().Error(err)
	s.Assert().EqualError(err, "closed pool")
}

func createTable(ctx context.Context, db *TestDB) error {
	const schemaDDL = `
		CREATE TABLE IF NOT EXISTS accounts
		(
		    account_id		UUID,
			account_name 	VARCHAR(255)  NOT NULL,
		    PRIMARY KEY (account_id)
		);
	`
	_, err := db.Exec(ctx, schemaDDL)
	return err
}

func insertInto(ctx context.Context, db *TestDB, account *account) error {
	const insertSQL = `INSERT INTO accounts(account_id, account_name) VALUES($1, $2);`
	_, err := db.Exec(ctx, insertSQL, account.AccountID, account.AccountName)
	return err
}
