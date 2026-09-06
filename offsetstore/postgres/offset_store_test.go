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
	"testing"
	"time"

	pgxmock "github.com/pashagolub/pgxmock/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/ego/v4/egopb"
	"github.com/tochemey/ego/v4/offsetstore"
)

func TestPostgresOffsetStore(t *testing.T) {
	t.Run("testNewOffsetStore", func(t *testing.T) {
		estore := NewOffsetStore(testConfig())
		assert.NotNil(t, estore)
		var p interface{} = estore
		_, ok := p.(offsetstore.OffsetStore)
		assert.True(t, ok)
	})
	t.Run("testConnect:happy path", func(t *testing.T) {
		ctx := context.TODO()
		store := NewOffsetStore(testConfig())
		assert.NotNil(t, store)
		err := store.Connect(ctx)
		assert.NoError(t, err)
		err = store.Disconnect(ctx)
		assert.NoError(t, err)
	})
	t.Run("testConnect:database does not exist", func(t *testing.T) {
		ctx := context.TODO()
		config := testConfig()
		config.DBName = "testDatabase"

		store := NewOffsetStore(config)
		assert.NotNil(t, store)
		err := store.Connect(ctx)
		assert.Error(t, err)
	})
	t.Run("testConnect:owned pool is rebuilt after Disconnect", func(t *testing.T) {
		ctx := context.TODO()
		store := NewOffsetStore(testConfig())
		require.NoError(t, store.Connect(ctx))
		require.NoError(t, store.Disconnect(ctx))

		// the store rebuilds its pool on the next Connect
		require.NoError(t, store.Connect(ctx))
		require.NoError(t, store.Ping(ctx))
		require.NoError(t, store.Disconnect(ctx))
	})
	t.Run("testWithPool:pool is shared and left open", func(t *testing.T) {
		ctx := context.TODO()
		pool, err := newPool(ctx, testConfig().sanitize())
		require.NoError(t, err)
		defer pool.Close()

		db, err := dbHandle(ctx)
		require.NoError(t, err)
		schemaUtil := NewSchemaUtils(db)
		require.NoError(t, schemaUtil.CreateTable(ctx))

		store := NewOffsetStoreWithPool(pool)
		require.NoError(t, store.Connect(ctx))
		require.NoError(t, store.WriteOffset(ctx, &egopb.Offset{
			ShardNumber:    1,
			ProjectionName: "some-projection",
			Value:          10,
			Timestamp:      time.Now().UnixMilli(),
		}))

		// disconnecting the store must leave the caller's pool open
		require.NoError(t, store.Disconnect(ctx))
		require.NoError(t, pool.Ping(ctx))

		// a second store on the same pool keeps working
		other := NewOffsetStoreWithPool(pool)
		require.NoError(t, other.Connect(ctx))
		current, err := other.GetCurrentOffset(ctx, &egopb.ProjectionId{ProjectionName: "some-projection", ShardNumber: 1})
		require.NoError(t, err)
		require.NotNil(t, current)
		assert.EqualValues(t, 10, current.GetValue())
		require.NoError(t, other.Disconnect(ctx))

		require.NoError(t, schemaUtil.DropTable(ctx))
	})
	t.Run("testWriteOffset", func(t *testing.T) {
		ctx := context.TODO()

		db, err := dbHandle(ctx)
		require.NoError(t, err)
		schemaUtil := NewSchemaUtils(db)
		err = schemaUtil.CreateTable(ctx)
		require.NoError(t, err)

		store := NewOffsetStore(testConfig())
		assert.NotNil(t, store)
		err = store.Connect(ctx)
		require.NoError(t, err)

		offset := &egopb.Offset{
			ShardNumber:    uint64(9),
			ProjectionName: "some-projection",
			Value:          int64(10),
			Timestamp:      time.Now().UnixMilli(),
		}

		// write the offset
		assert.NoError(t, store.WriteOffset(ctx, offset))

		// get the current shard offset
		current, err := store.GetCurrentOffset(ctx, &egopb.ProjectionId{
			ProjectionName: "some-projection",
			ShardNumber:    uint64(9),
		})
		require.NoError(t, err)
		assert.True(t, proto.Equal(offset, current))

		// an unknown projection yields no offset
		missing, err := store.GetCurrentOffset(ctx, &egopb.ProjectionId{
			ProjectionName: "unknown-projection",
			ShardNumber:    uint64(9),
		})
		require.NoError(t, err)
		assert.Nil(t, missing)

		err = schemaUtil.DropTable(ctx)
		assert.NoError(t, err)

		err = store.Disconnect(ctx)
		assert.NoError(t, err)
	})
	t.Run("testResetOffset", func(t *testing.T) {
		ctx := context.TODO()

		db, err := dbHandle(ctx)
		require.NoError(t, err)
		schemaUtil := NewSchemaUtils(db)
		err = schemaUtil.CreateTable(ctx)
		require.NoError(t, err)

		store := NewOffsetStore(testConfig())
		assert.NotNil(t, store)
		err = store.Connect(ctx)
		require.NoError(t, err)

		projection1 := "projection-1"
		projection2 := "projection-2"
		ts := time.Now().UnixMilli()
		// write offset into 10 shards for projection1
		for i := 0; i < 10; i++ {
			shard := uint64(i + 1)
			offset := &egopb.Offset{
				ShardNumber:    shard,
				ProjectionName: projection1,
				Value:          int64(i + 1),
				Timestamp:      ts,
			}
			// write the offset
			assert.NoError(t, store.WriteOffset(ctx, offset))
		}

		// write offset into 10 for projection2
		// this only to have multiple records in the storage
		for i := 0; i < 10; i++ {
			shard := uint64(i + 1)
			offset := &egopb.Offset{
				ShardNumber:    shard,
				ProjectionName: projection2,
				Value:          2 * int64(i+1),
				Timestamp:      ts,
			}
			// write the offset
			assert.NoError(t, store.WriteOffset(ctx, offset))
		}

		// get the current shard offset
		current, err := store.GetCurrentOffset(ctx, &egopb.ProjectionId{
			ProjectionName: projection1,
			ShardNumber:    uint64(9),
		})
		require.NoError(t, err)
		expected := &egopb.Offset{
			ShardNumber:    uint64(9),
			ProjectionName: projection1,
			Value:          int64(9),
			Timestamp:      ts,
		}
		assert.True(t, proto.Equal(expected, current))

		// reset the projection 1 offset
		require.NoError(t, store.ResetOffset(ctx, projection1, int64(1000)))
		current, err = store.GetCurrentOffset(ctx, &egopb.ProjectionId{
			ProjectionName: projection1,
			ShardNumber:    uint64(9),
		})
		require.NoError(t, err)
		assert.EqualValues(t, 1000, current.GetValue())

		// projection 2 is left untouched
		current, err = store.GetCurrentOffset(ctx, &egopb.ProjectionId{
			ProjectionName: projection2,
			ShardNumber:    uint64(9),
		})
		require.NoError(t, err)
		assert.EqualValues(t, 18, current.GetValue())

		err = schemaUtil.DropTable(ctx)
		assert.NoError(t, err)

		err = store.Disconnect(ctx)
		assert.NoError(t, err)
	})
}

// newMockStore creates an offset store backed by a pgxmock pool
func newMockStore(t *testing.T) (*OffsetStore, pgxmock.PgxPoolIface) {
	t.Helper()
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	t.Cleanup(mock.Close)
	return NewOffsetStoreWithPool(mock), mock
}

// newConnectedMockStore creates an offset store backed by a pgxmock pool and connects it
func newConnectedMockStore(t *testing.T) (*OffsetStore, pgxmock.PgxPoolIface) {
	t.Helper()
	store, mock := newMockStore(t)
	mock.ExpectPing()
	require.NoError(t, store.Connect(context.Background()))
	return store, mock
}

func TestOffsetStoreNotConnected(t *testing.T) {
	ctx := context.Background()
	store, _ := newMockStore(t)

	t.Run("WriteOffset", func(t *testing.T) {
		err := store.WriteOffset(ctx, &egopb.Offset{ProjectionName: "p", ShardNumber: 1, Value: 1})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not connected")
	})

	t.Run("GetCurrentOffset", func(t *testing.T) {
		_, err := store.GetCurrentOffset(ctx, &egopb.ProjectionId{ProjectionName: "p", ShardNumber: 1})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not connected")
	})

	t.Run("ResetOffset", func(t *testing.T) {
		err := store.ResetOffset(ctx, "p", 0)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not connected")
	})
}

func TestOffsetStoreConnectionLifecycle(t *testing.T) {
	ctx := context.Background()

	t.Run("Connect pings the provided pool", func(t *testing.T) {
		store, mock := newMockStore(t)
		mock.ExpectPing()
		require.NoError(t, store.Connect(ctx))
		_, err := store.activePool()
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("Connect already connected is no-op", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)
		require.NoError(t, store.Connect(ctx))
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("Connect propagates ping error", func(t *testing.T) {
		store, mock := newMockStore(t)
		mock.ExpectPing().WillReturnError(errors.New("connection refused"))
		err := store.Connect(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "connection refused")
		_, err = store.activePool()
		assert.ErrorIs(t, err, errNotConnected)
	})

	t.Run("Connect without pool fails", func(t *testing.T) {
		store := NewOffsetStoreWithPool(nil)
		err := store.Connect(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "pool is not defined")
	})

	t.Run("Disconnect when not connected is no-op", func(t *testing.T) {
		store, _ := newMockStore(t)
		assert.NoError(t, store.Disconnect(ctx))
	})

	t.Run("Disconnect leaves the provided pool open", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)
		require.NoError(t, store.Disconnect(ctx))
		_, err := store.activePool()
		assert.ErrorIs(t, err, errNotConnected)

		// the pool is still usable: the store can reconnect on it
		mock.ExpectPing()
		require.NoError(t, store.Connect(ctx))
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("Ping when connected delegates to the pool", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)
		mock.ExpectPing()
		assert.NoError(t, store.Ping(ctx))
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("Ping when connected propagates pool error", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)
		mock.ExpectPing().WillReturnError(errors.New("ping failed"))
		err := store.Ping(ctx)
		require.Error(t, err)
		assert.EqualError(t, err, "ping failed")
	})

	t.Run("Ping when not connected calls Connect", func(t *testing.T) {
		store, mock := newMockStore(t)
		mock.ExpectPing()
		assert.NoError(t, store.Ping(ctx))
		_, err := store.activePool()
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("Ping when not connected propagates Connect error", func(t *testing.T) {
		store, mock := newMockStore(t)
		mock.ExpectPing().WillReturnError(errors.New("connect failed"))
		err := store.Ping(ctx)
		require.Error(t, err)
		_, err = store.activePool()
		assert.ErrorIs(t, err, errNotConnected)
	})
}

func TestWriteOffsetUnit(t *testing.T) {
	ctx := context.Background()

	t.Run("nil offset is rejected", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)
		err := store.WriteOffset(ctx, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "offset record is not defined")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty offset is rejected", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)
		err := store.WriteOffset(ctx, &egopb.Offset{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "offset record is not defined")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("Exec error", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)
		mock.ExpectExec("INSERT INTO offsets_store").
			WithArgs("p1", uint64(2), int64(42), int64(1000)).
			WillReturnError(errors.New("exec failed"))

		err := store.WriteOffset(ctx, &egopb.Offset{ProjectionName: "p1", ShardNumber: 2, Value: 42, Timestamp: 1000})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to write offset")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("offset is upserted", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)
		mock.ExpectExec("INSERT INTO offsets_store").
			WithArgs("p1", uint64(2), int64(42), int64(1000)).
			WillReturnResult(pgxmock.NewResult("INSERT", 1))

		require.NoError(t, store.WriteOffset(ctx, &egopb.Offset{ProjectionName: "p1", ShardNumber: 2, Value: 42, Timestamp: 1000}))
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestGetCurrentOffsetUnit(t *testing.T) {
	ctx := context.Background()
	projectionID := &egopb.ProjectionId{ProjectionName: "p1", ShardNumber: 2}

	t.Run("Query error", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)
		mock.ExpectQuery("FROM offsets_store WHERE projection_name").
			WithArgs("p1", uint64(2)).
			WillReturnError(errors.New("select failed"))

		_, err := store.GetCurrentOffset(ctx, projectionID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to fetch the current offset from the database")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty result returns nil", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)
		mock.ExpectQuery("FROM offsets_store WHERE projection_name").
			WithArgs("p1", uint64(2)).
			WillReturnRows(pgxmock.NewRows(columns))

		offset, err := store.GetCurrentOffset(ctx, projectionID)
		assert.NoError(t, err)
		assert.Nil(t, offset)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("row is scanned into an offset", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)
		mock.ExpectQuery("FROM offsets_store WHERE projection_name").
			WithArgs("p1", uint64(2)).
			WillReturnRows(pgxmock.NewRows(columns).AddRow("p1", uint64(2), int64(42), int64(1000)))

		offset, err := store.GetCurrentOffset(ctx, projectionID)
		require.NoError(t, err)
		expected := &egopb.Offset{ProjectionName: "p1", ShardNumber: 2, Value: 42, Timestamp: 1000}
		assert.True(t, proto.Equal(expected, offset))
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestResetOffsetUnit(t *testing.T) {
	ctx := context.Background()

	t.Run("Exec error", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)
		mock.ExpectExec("UPDATE offsets_store SET").
			WithArgs(int64(7), pgxmock.AnyArg(), "p1").
			WillReturnError(errors.New("exec failed"))

		err := store.ResetOffset(ctx, "p1", 7)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to reset offset")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("offsets are reset", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)
		mock.ExpectExec("UPDATE offsets_store SET").
			WithArgs(int64(7), pgxmock.AnyArg(), "p1").
			WillReturnResult(pgxmock.NewResult("UPDATE", 10))

		require.NoError(t, store.ResetOffset(ctx, "p1", 7))
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}
