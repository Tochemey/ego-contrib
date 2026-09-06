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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/ego/v4/egopb"
	"github.com/tochemey/ego/v4/persistence"
	"google.golang.org/protobuf/proto"
)

func TestNewDurableStore(t *testing.T) {
	store := NewDurableStore(&Config{})
	require.NotNil(t, store)

	var iface any = store
	_, ok := iface.(persistence.StateStore)
	assert.True(t, ok, "the store must implement persistence.StateStore")
}

func TestConnectionLifecycle(t *testing.T) {
	ctx := context.Background()

	t.Run("an owned handle is opened by Connect and closed by Disconnect", func(t *testing.T) {
		store := NewDurableStore(testConfig(t))

		require.NoError(t, store.Connect(ctx))
		require.NoError(t, store.Ping(ctx))

		require.NoError(t, store.Disconnect(ctx))
		_, err := store.activeDB()
		assert.ErrorIs(t, err, errNotConnected)
	})

	t.Run("an owned handle is reopened on the next Connect", func(t *testing.T) {
		store := NewDurableStore(testConfig(t))

		require.NoError(t, store.Connect(ctx))
		require.NoError(t, store.Disconnect(ctx))

		require.NoError(t, store.Connect(ctx))
		require.NoError(t, store.Ping(ctx))
		require.NoError(t, store.Disconnect(ctx))
	})

	t.Run("Connect is a no-op when already connected", func(t *testing.T) {
		store := newConnectedStore(t)
		assert.NoError(t, store.Connect(ctx))
	})

	t.Run("Disconnect is a no-op when not connected", func(t *testing.T) {
		store := NewDurableStore(testConfig(t))
		assert.NoError(t, store.Disconnect(ctx))
	})

	t.Run("Connect reports a database that cannot be opened", func(t *testing.T) {
		store := NewDurableStore(&Config{DBPath: t.TempDir() + "/missing-directory/states.db"})
		assert.Error(t, store.Connect(ctx))
	})

	t.Run("Ping connects when the store is not connected yet", func(t *testing.T) {
		store := NewDurableStore(testConfig(t))
		require.NoError(t, store.Ping(ctx))

		_, err := store.activeDB()
		assert.NoError(t, err)
		require.NoError(t, store.Disconnect(ctx))
	})

	t.Run("a supplied handle is pinged by Connect and left open by Disconnect", func(t *testing.T) {
		db := newTestDB(t)
		store := NewDurableStoreWithSqlite(db)

		require.NoError(t, store.Connect(ctx))
		require.NoError(t, store.WriteState(ctx, newTestState(t, "entity-1", 1, "state-v1")))
		require.NoError(t, store.Disconnect(ctx))

		// the handle is still usable after the store let go of it
		require.NoError(t, db.PingContext(ctx))
		assert.Equal(t, 1, countRows(t, db))

		// and a second store can run on it
		other := NewDurableStoreWithSqlite(db)
		require.NoError(t, other.Connect(ctx))
		latest, err := other.GetLatestState(ctx, "entity-1")
		require.NoError(t, err)
		require.NotNil(t, latest)
		assert.EqualValues(t, 1, latest.GetVersionNumber())
		require.NoError(t, other.Disconnect(ctx))
	})

	t.Run("Connect fails when no handle was supplied", func(t *testing.T) {
		store := NewDurableStoreWithSqlite(nil)
		err := store.Connect(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "database handle is not defined")
	})

	t.Run("Connect reports a handle that cannot be reached", func(t *testing.T) {
		db := newTestDB(t)
		require.NoError(t, db.Close())

		store := NewDurableStoreWithSqlite(db)
		err := store.Connect(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to ping the database")
	})
}

func TestOperationsRequireAConnection(t *testing.T) {
	ctx := context.Background()
	store := NewDurableStore(testConfig(t))

	t.Run("WriteState", func(t *testing.T) {
		assert.ErrorIs(t, store.WriteState(ctx, newTestState(t, "p1", 1, "state")), errNotConnected)
	})
	t.Run("GetLatestState", func(t *testing.T) {
		_, err := store.GetLatestState(ctx, "p1")
		assert.ErrorIs(t, err, errNotConnected)
	})
}

func TestWriteAndGetLatestState(t *testing.T) {
	ctx := context.Background()
	store := newConnectedStore(t)
	persistenceID := "entity-1"

	t.Run("an unknown entity has no state", func(t *testing.T) {
		state, err := store.GetLatestState(ctx, persistenceID)
		require.NoError(t, err)
		assert.Nil(t, state)
	})

	first := newTestState(t, persistenceID, 1, "state-v1")

	t.Run("a state is written and read back", func(t *testing.T) {
		require.NoError(t, store.WriteState(ctx, first))

		state, err := store.GetLatestState(ctx, persistenceID)
		require.NoError(t, err)
		assert.True(t, proto.Equal(first, state))
	})

	t.Run("a new version replaces the single row", func(t *testing.T) {
		second := newTestState(t, persistenceID, 2, "state-v2")
		require.NoError(t, store.WriteState(ctx, second))

		db, err := store.activeDB()
		require.NoError(t, err)
		assert.Equal(t, 1, countRows(t, db))

		state, err := store.GetLatestState(ctx, persistenceID)
		require.NoError(t, err)
		assert.True(t, proto.Equal(second, state))
	})

	t.Run("a nil or empty state is ignored", func(t *testing.T) {
		require.NoError(t, store.WriteState(ctx, nil))
		require.NoError(t, store.WriteState(ctx, &egopb.DurableState{}))

		db, err := store.activeDB()
		require.NoError(t, err)
		assert.Equal(t, 1, countRows(t, db))
	})
}

func TestQueryErrorsAreReported(t *testing.T) {
	ctx := context.Background()
	store := newConnectedStore(t)

	db, err := store.activeDB()
	require.NoError(t, err)
	dropSchema(t, db)

	t.Run("WriteState", func(t *testing.T) {
		err := store.WriteState(ctx, newTestState(t, "p1", 1, "state"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to record durable state")
	})
	t.Run("GetLatestState", func(t *testing.T) {
		_, err := store.GetLatestState(ctx, "p1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to fetch the latest durable state from the database")
	})
}

func TestUnmarshallingFailureIsReported(t *testing.T) {
	ctx := context.Background()
	store := newConnectedStore(t)

	db, err := store.activeDB()
	require.NoError(t, err)

	// a manifest no protobuf registry can resolve
	_, err = db.ExecContext(ctx,
		"INSERT INTO "+tableName+" (persistence_id, version_number, state_payload, state_manifest, timestamp, shard_number) VALUES (?,?,?,?,?,?)",
		"entity-1", 1, []byte("payload"), "unknown.Message", 100, 1)
	require.NoError(t, err)

	_, err = store.GetLatestState(ctx, "entity-1")
	assert.Error(t, err)
}
