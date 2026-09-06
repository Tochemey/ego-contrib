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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/ego/v4/egopb"
	"github.com/tochemey/ego/v4/offsetstore"
)

func TestNewOffsetStore(t *testing.T) {
	store := NewOffsetStore(&Config{})
	require.NotNil(t, store)

	var iface any = store
	_, ok := iface.(offsetstore.OffsetStore)
	assert.True(t, ok, "the store must implement offsetstore.OffsetStore")
}

func TestConnectionLifecycle(t *testing.T) {
	ctx := context.Background()

	t.Run("an owned handle is opened by Connect and closed by Disconnect", func(t *testing.T) {
		store := NewOffsetStore(testConfig(t))

		require.NoError(t, store.Connect(ctx))
		require.NoError(t, store.Ping(ctx))

		require.NoError(t, store.Disconnect(ctx))
		_, err := store.activeDB()
		assert.ErrorIs(t, err, errNotConnected)
	})

	t.Run("an owned handle is reopened on the next Connect", func(t *testing.T) {
		store := NewOffsetStore(testConfig(t))

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
		store := NewOffsetStore(testConfig(t))
		assert.NoError(t, store.Disconnect(ctx))
	})

	t.Run("Connect reports a database that cannot be opened", func(t *testing.T) {
		store := NewOffsetStore(&Config{DBPath: t.TempDir() + "/missing-directory/offsets.db"})
		assert.Error(t, store.Connect(ctx))
	})

	t.Run("Ping connects when the store is not connected yet", func(t *testing.T) {
		store := NewOffsetStore(testConfig(t))
		require.NoError(t, store.Ping(ctx))

		_, err := store.activeDB()
		assert.NoError(t, err)
		require.NoError(t, store.Disconnect(ctx))
	})

	t.Run("a supplied handle is pinged by Connect and left open by Disconnect", func(t *testing.T) {
		db := newTestDB(t)
		store := NewOffsetStoreWithSqlite(db)

		require.NoError(t, store.Connect(ctx))
		require.NoError(t, store.WriteOffset(ctx, &egopb.Offset{ProjectionName: "projection", ShardNumber: 1, Value: 10, Timestamp: 1}))
		require.NoError(t, store.Disconnect(ctx))

		// the handle is still usable after the store let go of it
		require.NoError(t, db.PingContext(ctx))
		assert.Equal(t, 1, countRows(t, db))

		// and a second store can run on it
		other := NewOffsetStoreWithSqlite(db)
		require.NoError(t, other.Connect(ctx))
		current, err := other.GetCurrentOffset(ctx, &egopb.ProjectionId{ProjectionName: "projection", ShardNumber: 1})
		require.NoError(t, err)
		require.NotNil(t, current)
		assert.EqualValues(t, 10, current.GetValue())
		require.NoError(t, other.Disconnect(ctx))
	})

	t.Run("Connect fails when no handle was supplied", func(t *testing.T) {
		store := NewOffsetStoreWithSqlite(nil)
		err := store.Connect(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "database handle is not defined")
	})

	t.Run("Connect reports a handle that cannot be reached", func(t *testing.T) {
		db := newTestDB(t)
		require.NoError(t, db.Close())

		store := NewOffsetStoreWithSqlite(db)
		err := store.Connect(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to ping the database")
	})
}

func TestOperationsRequireAConnection(t *testing.T) {
	ctx := context.Background()
	store := NewOffsetStore(testConfig(t))

	t.Run("WriteOffset", func(t *testing.T) {
		assert.ErrorIs(t, store.WriteOffset(ctx, &egopb.Offset{ProjectionName: "p", ShardNumber: 1, Value: 1}), errNotConnected)
	})
	t.Run("GetCurrentOffset", func(t *testing.T) {
		_, err := store.GetCurrentOffset(ctx, &egopb.ProjectionId{ProjectionName: "p", ShardNumber: 1})
		assert.ErrorIs(t, err, errNotConnected)
	})
	t.Run("ResetOffset", func(t *testing.T) {
		assert.ErrorIs(t, store.ResetOffset(ctx, "p", 0), errNotConnected)
	})
}

func TestWriteAndGetCurrentOffset(t *testing.T) {
	ctx := context.Background()
	store := newConnectedStore(t)
	projectionID := &egopb.ProjectionId{ProjectionName: "accounts-projection", ShardNumber: 9}

	t.Run("an unknown projection has no offset", func(t *testing.T) {
		current, err := store.GetCurrentOffset(ctx, projectionID)
		require.NoError(t, err)
		assert.Nil(t, current)
	})

	offset := &egopb.Offset{ProjectionName: "accounts-projection", ShardNumber: 9, Value: 10, Timestamp: time.Now().UnixMilli()}

	t.Run("an offset is written and read back", func(t *testing.T) {
		require.NoError(t, store.WriteOffset(ctx, offset))

		current, err := store.GetCurrentOffset(ctx, projectionID)
		require.NoError(t, err)
		assert.True(t, proto.Equal(offset, current))
	})

	t.Run("a new value replaces the single row", func(t *testing.T) {
		updated := &egopb.Offset{ProjectionName: "accounts-projection", ShardNumber: 9, Value: 42, Timestamp: offset.GetTimestamp() + 1}
		require.NoError(t, store.WriteOffset(ctx, updated))

		db, err := store.activeDB()
		require.NoError(t, err)
		assert.Equal(t, 1, countRows(t, db))

		current, err := store.GetCurrentOffset(ctx, projectionID)
		require.NoError(t, err)
		assert.True(t, proto.Equal(updated, current))
	})

	t.Run("a nil or empty offset is rejected", func(t *testing.T) {
		err := store.WriteOffset(ctx, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "offset record is not defined")

		err = store.WriteOffset(ctx, &egopb.Offset{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "offset record is not defined")
	})
}

func TestResetOffset(t *testing.T) {
	ctx := context.Background()
	store := newConnectedStore(t)

	ts := time.Now().UnixMilli()
	for shard := uint64(1); shard <= 10; shard++ {
		require.NoError(t, store.WriteOffset(ctx, &egopb.Offset{ProjectionName: "projection-1", ShardNumber: shard, Value: int64(shard), Timestamp: ts}))
		require.NoError(t, store.WriteOffset(ctx, &egopb.Offset{ProjectionName: "projection-2", ShardNumber: shard, Value: int64(2 * shard), Timestamp: ts}))
	}

	require.NoError(t, store.ResetOffset(ctx, "projection-1", 1000))

	t.Run("every shard of the projection carries the new value", func(t *testing.T) {
		for shard := uint64(1); shard <= 10; shard++ {
			current, err := store.GetCurrentOffset(ctx, &egopb.ProjectionId{ProjectionName: "projection-1", ShardNumber: shard})
			require.NoError(t, err)
			assert.EqualValues(t, 1000, current.GetValue())
		}
	})

	t.Run("other projections are left untouched", func(t *testing.T) {
		current, err := store.GetCurrentOffset(ctx, &egopb.ProjectionId{ProjectionName: "projection-2", ShardNumber: 9})
		require.NoError(t, err)
		assert.EqualValues(t, 18, current.GetValue())
	})
}

func TestQueryErrorsAreReported(t *testing.T) {
	ctx := context.Background()
	store := newConnectedStore(t)

	db, err := store.activeDB()
	require.NoError(t, err)
	dropSchema(t, db)

	t.Run("WriteOffset", func(t *testing.T) {
		err := store.WriteOffset(ctx, &egopb.Offset{ProjectionName: "p", ShardNumber: 1, Value: 1})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to write offset")
	})
	t.Run("GetCurrentOffset", func(t *testing.T) {
		_, err := store.GetCurrentOffset(ctx, &egopb.ProjectionId{ProjectionName: "p", ShardNumber: 1})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to fetch the current offset from the database")
	})
	t.Run("ResetOffset", func(t *testing.T) {
		err := store.ResetOffset(ctx, "p", 0)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to reset offset")
	})
}
