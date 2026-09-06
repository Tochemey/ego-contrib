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
	"github.com/tochemey/ego/v4/egopb"
	"github.com/tochemey/ego/v4/persistence"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

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

func TestPostgresDurableStore(t *testing.T) {
	t.Run("testNewDurableStore", func(t *testing.T) {
		store := NewDurableStore(testConfig())
		assert.NotNil(t, store)
		var p interface{} = store
		_, ok := p.(persistence.StateStore)
		assert.True(t, ok)
	})
	t.Run("testConnect:happy path", func(t *testing.T) {
		ctx := context.TODO()
		store := NewDurableStore(testConfig())
		assert.NotNil(t, store)
		err := store.Connect(ctx)
		assert.NoError(t, err)
		err = store.Disconnect(ctx)
		assert.NoError(t, err)
	})
	t.Run("testConnect:database does not exist", func(t *testing.T) {
		ctx := context.TODO()
		config := testConfig()
		config.DBName = "nonexistent"

		store := NewDurableStore(config)
		assert.NotNil(t, store)
		err := store.Connect(ctx)
		assert.Error(t, err)
	})
	t.Run("testConnect:owned pool is rebuilt after Disconnect", func(t *testing.T) {
		ctx := context.TODO()
		store := NewDurableStore(testConfig())
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

		store := NewDurableStoreWithPool(pool)
		require.NoError(t, store.Connect(ctx))
		require.NoError(t, store.WriteState(ctx, newTestState(t, "entity-1", 1, "state-v1")))

		// disconnecting the store must leave the caller's pool open
		require.NoError(t, store.Disconnect(ctx))
		require.NoError(t, pool.Ping(ctx))

		// a second store on the same pool keeps working
		other := NewDurableStoreWithPool(pool)
		require.NoError(t, other.Connect(ctx))
		latest, err := other.GetLatestState(ctx, "entity-1")
		require.NoError(t, err)
		require.NotNil(t, latest)
		assert.EqualValues(t, 1, latest.GetVersionNumber())
		require.NoError(t, other.Disconnect(ctx))

		require.NoError(t, schemaUtil.DropTable(ctx))
	})
	t.Run("testWriteAndGetLatestState", func(t *testing.T) {
		ctx := context.TODO()

		db, err := dbHandle(ctx)
		require.NoError(t, err)
		schemaUtil := NewSchemaUtils(db)
		require.NoError(t, schemaUtil.CreateTable(ctx))

		store := NewDurableStore(testConfig())
		require.NoError(t, store.Connect(ctx))

		persistenceID := "entity-2"

		// no state yet
		missing, err := store.GetLatestState(ctx, persistenceID)
		require.NoError(t, err)
		assert.Nil(t, missing)

		// write the first version
		first := newTestState(t, persistenceID, 1, "state-v1")
		require.NoError(t, store.WriteState(ctx, first))

		latest, err := store.GetLatestState(ctx, persistenceID)
		require.NoError(t, err)
		require.NotNil(t, latest)
		assert.True(t, proto.Equal(first, latest))

		// writing a new version upserts the single row
		second := newTestState(t, persistenceID, 2, "state-v2")
		require.NoError(t, store.WriteState(ctx, second))

		count, err := db.Count(ctx, tableName)
		require.NoError(t, err)
		assert.Equal(t, 1, count)

		latest, err = store.GetLatestState(ctx, persistenceID)
		require.NoError(t, err)
		require.NotNil(t, latest)
		assert.True(t, proto.Equal(second, latest))

		require.NoError(t, schemaUtil.DropTable(ctx))
		require.NoError(t, store.Disconnect(ctx))
	})
}

// newMockStore creates a durable store backed by a pgxmock pool
func newMockStore(t *testing.T) (*DurableStore, pgxmock.PgxPoolIface) {
	t.Helper()
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	t.Cleanup(mock.Close)
	return NewDurableStoreWithPool(mock), mock
}

// newConnectedMockStore creates a durable store backed by a pgxmock pool and connects it
func newConnectedMockStore(t *testing.T) (*DurableStore, pgxmock.PgxPoolIface) {
	t.Helper()
	store, mock := newMockStore(t)
	mock.ExpectPing()
	require.NoError(t, store.Connect(context.Background()))
	return store, mock
}

func TestDurableStoreNotConnected(t *testing.T) {
	ctx := context.Background()
	store, _ := newMockStore(t)

	t.Run("WriteState", func(t *testing.T) {
		err := store.WriteState(ctx, newTestState(t, "p1", 1, "state"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not connected")
	})

	t.Run("GetLatestState", func(t *testing.T) {
		_, err := store.GetLatestState(ctx, "p1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not connected")
	})
}

func TestDurableStoreConnectionLifecycle(t *testing.T) {
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
		store := NewDurableStoreWithPool(nil)
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

func TestWriteStateUnit(t *testing.T) {
	ctx := context.Background()

	t.Run("nil state is no-op", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)
		assert.NoError(t, store.WriteState(ctx, nil))
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty state is no-op", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)
		assert.NoError(t, store.WriteState(ctx, &egopb.DurableState{}))
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("Exec error", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)
		mock.ExpectExec("INSERT INTO states_store").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnError(errors.New("exec failed"))

		err := store.WriteState(ctx, newTestState(t, "p1", 1, "state"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to record durable state")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("state is upserted", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)
		state := newTestState(t, "p1", 3, "state")
		mock.ExpectExec("INSERT INTO states_store").
			WithArgs("p1", uint64(3), pgxmock.AnyArg(), "google.protobuf.Any", state.GetTimestamp(), uint64(1)).
			WillReturnResult(pgxmock.NewResult("INSERT", 1))

		require.NoError(t, store.WriteState(ctx, state))
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestGetLatestStateUnit(t *testing.T) {
	ctx := context.Background()

	t.Run("Query error", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)
		mock.ExpectQuery("FROM states_store WHERE persistence_id").
			WithArgs("p1").
			WillReturnError(errors.New("select failed"))

		_, err := store.GetLatestState(ctx, "p1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to fetch the latest durable state from the database")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty result returns nil", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)
		mock.ExpectQuery("FROM states_store WHERE persistence_id").
			WithArgs("p1").
			WillReturnRows(pgxmock.NewRows(columns))

		state, err := store.GetLatestState(ctx, "p1")
		assert.NoError(t, err)
		assert.Nil(t, state)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("row is scanned into a durable state", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)
		expected := newTestState(t, "p1", 5, "state")
		payload, err := proto.Marshal(expected.GetResultingState())
		require.NoError(t, err)

		mock.ExpectQuery("FROM states_store WHERE persistence_id").
			WithArgs("p1").
			WillReturnRows(pgxmock.NewRows(columns).AddRow(
				expected.GetPersistenceId(),
				expected.GetVersionNumber(),
				payload,
				"google.protobuf.Any",
				expected.GetTimestamp(),
				expected.GetShard(),
			))

		state, err := store.GetLatestState(ctx, "p1")
		require.NoError(t, err)
		assert.True(t, proto.Equal(expected, state))
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}
