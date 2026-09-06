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

func TestNewSnapshotStore(t *testing.T) {
	store := NewSnapshotStore(&Config{})
	require.NotNil(t, store)

	var iface any = store
	_, ok := iface.(persistence.SnapshotStore)
	assert.True(t, ok, "the store must implement persistence.SnapshotStore")
}

func TestConnectionLifecycle(t *testing.T) {
	ctx := context.Background()

	t.Run("an owned handle is opened by Connect and closed by Disconnect", func(t *testing.T) {
		store := NewSnapshotStore(testConfig(t))

		require.NoError(t, store.Connect(ctx))
		require.NoError(t, store.Ping(ctx))

		require.NoError(t, store.Disconnect(ctx))
		_, err := store.activeDB()
		assert.ErrorIs(t, err, errNotConnected)
	})

	t.Run("an owned handle is reopened on the next Connect", func(t *testing.T) {
		store := NewSnapshotStore(testConfig(t))

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
		store := NewSnapshotStore(testConfig(t))
		assert.NoError(t, store.Disconnect(ctx))
	})

	t.Run("Connect reports a database that cannot be opened", func(t *testing.T) {
		store := NewSnapshotStore(&Config{DBPath: t.TempDir() + "/missing-directory/snapshots.db"})
		assert.Error(t, store.Connect(ctx))
	})

	t.Run("Ping connects when the store is not connected yet", func(t *testing.T) {
		store := NewSnapshotStore(testConfig(t))
		require.NoError(t, store.Ping(ctx))

		_, err := store.activeDB()
		assert.NoError(t, err)
		require.NoError(t, store.Disconnect(ctx))
	})

	t.Run("a supplied handle is pinged by Connect and left open by Disconnect", func(t *testing.T) {
		db := newTestDB(t)
		store := NewSnapshotStoreWithSqlite(db)

		require.NoError(t, store.Connect(ctx))
		require.NoError(t, store.WriteSnapshot(ctx, newTestSnapshot(t, "entity-1", 1, "state-v1")))
		require.NoError(t, store.Disconnect(ctx))

		// the handle is still usable after the store let go of it
		require.NoError(t, db.PingContext(ctx))
		assert.Equal(t, 1, countRows(t, db))

		// and a second store can run on it
		other := NewSnapshotStoreWithSqlite(db)
		require.NoError(t, other.Connect(ctx))
		latest, err := other.GetLatestSnapshot(ctx, "entity-1")
		require.NoError(t, err)
		require.NotNil(t, latest)
		assert.EqualValues(t, 1, latest.GetSequenceNumber())
		require.NoError(t, other.Disconnect(ctx))
	})

	t.Run("Connect fails when no handle was supplied", func(t *testing.T) {
		store := NewSnapshotStoreWithSqlite(nil)
		err := store.Connect(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "database handle is not defined")
	})

	t.Run("Connect reports a handle that cannot be reached", func(t *testing.T) {
		db := newTestDB(t)
		require.NoError(t, db.Close())

		store := NewSnapshotStoreWithSqlite(db)
		err := store.Connect(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to ping the database")
	})
}

func TestOperationsRequireAConnection(t *testing.T) {
	ctx := context.Background()
	store := NewSnapshotStore(testConfig(t))

	t.Run("WriteSnapshot", func(t *testing.T) {
		assert.ErrorIs(t, store.WriteSnapshot(ctx, newTestSnapshot(t, "p1", 1, "state")), errNotConnected)
	})
	t.Run("GetLatestSnapshot", func(t *testing.T) {
		_, err := store.GetLatestSnapshot(ctx, "p1")
		assert.ErrorIs(t, err, errNotConnected)
	})
	t.Run("DeleteSnapshots", func(t *testing.T) {
		assert.ErrorIs(t, store.DeleteSnapshots(ctx, "p1", 10), errNotConnected)
	})
}

func TestWriteAndGetLatestSnapshot(t *testing.T) {
	ctx := context.Background()
	store := newConnectedStore(t)
	persistenceID := "entity-1"

	t.Run("an unknown entity has no snapshot", func(t *testing.T) {
		snapshot, err := store.GetLatestSnapshot(ctx, persistenceID)
		require.NoError(t, err)
		assert.Nil(t, snapshot)
	})

	t.Run("the latest snapshot has the highest sequence number", func(t *testing.T) {
		require.NoError(t, store.WriteSnapshot(ctx, newTestSnapshot(t, persistenceID, 5, "state-v5")))
		latest := newTestSnapshot(t, persistenceID, 10, "state-v10")
		require.NoError(t, store.WriteSnapshot(ctx, latest))

		snapshot, err := store.GetLatestSnapshot(ctx, persistenceID)
		require.NoError(t, err)
		assert.True(t, proto.Equal(latest, snapshot))
	})

	t.Run("writing the same sequence number replaces the row", func(t *testing.T) {
		replaced := newTestSnapshot(t, persistenceID, 10, "state-v10-bis")
		replaced.EncryptionKeyId = "key-1"
		replaced.IsEncrypted = true
		require.NoError(t, store.WriteSnapshot(ctx, replaced))

		db, err := store.activeDB()
		require.NoError(t, err)
		assert.Equal(t, 2, countRows(t, db))

		snapshot, err := store.GetLatestSnapshot(ctx, persistenceID)
		require.NoError(t, err)
		assert.True(t, proto.Equal(replaced, snapshot))
	})

	t.Run("a nil or empty snapshot is ignored", func(t *testing.T) {
		require.NoError(t, store.WriteSnapshot(ctx, nil))
		require.NoError(t, store.WriteSnapshot(ctx, &egopb.Snapshot{}))

		db, err := store.activeDB()
		require.NoError(t, err)
		assert.Equal(t, 2, countRows(t, db))
	})
}

func TestDeleteSnapshots(t *testing.T) {
	ctx := context.Background()
	store := newConnectedStore(t)
	persistenceID := "entity-2"

	for _, seq := range []uint64{5, 10, 15, 20} {
		require.NoError(t, store.WriteSnapshot(ctx, newTestSnapshot(t, persistenceID, seq, "state")))
	}

	t.Run("snapshots up to the sequence number are removed", func(t *testing.T) {
		require.NoError(t, store.DeleteSnapshots(ctx, persistenceID, 10))

		db, err := store.activeDB()
		require.NoError(t, err)
		assert.Equal(t, 2, countRows(t, db))

		latest, err := store.GetLatestSnapshot(ctx, persistenceID)
		require.NoError(t, err)
		assert.EqualValues(t, 20, latest.GetSequenceNumber())
	})

	t.Run("other entities are left untouched", func(t *testing.T) {
		require.NoError(t, store.WriteSnapshot(ctx, newTestSnapshot(t, "entity-3", 1, "state")))
		require.NoError(t, store.DeleteSnapshots(ctx, persistenceID, 100))

		latest, err := store.GetLatestSnapshot(ctx, "entity-3")
		require.NoError(t, err)
		require.NotNil(t, latest)
	})
}

func TestQueryErrorsAreReported(t *testing.T) {
	ctx := context.Background()
	store := newConnectedStore(t)

	db, err := store.activeDB()
	require.NoError(t, err)
	dropSchema(t, db)

	t.Run("WriteSnapshot", func(t *testing.T) {
		err := store.WriteSnapshot(ctx, newTestSnapshot(t, "p1", 1, "state"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to write snapshot")
	})
	t.Run("GetLatestSnapshot", func(t *testing.T) {
		_, err := store.GetLatestSnapshot(ctx, "p1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to fetch the latest snapshot from the database")
	})
	t.Run("DeleteSnapshots", func(t *testing.T) {
		err := store.DeleteSnapshots(ctx, "p1", 10)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to delete snapshots")
	})
}

func TestUnmarshallingFailureIsReported(t *testing.T) {
	ctx := context.Background()
	store := newConnectedStore(t)

	db, err := store.activeDB()
	require.NoError(t, err)

	// a manifest no protobuf registry can resolve
	_, err = db.ExecContext(ctx,
		"INSERT INTO "+tableName+" (persistence_id, sequence_number, state_payload, state_manifest, timestamp, encryption_key_id, is_encrypted) VALUES (?,?,?,?,?,?,?)",
		"entity-1", 1, []byte("payload"), "unknown.Message", 100, "", false)
	require.NoError(t, err)

	_, err = store.GetLatestSnapshot(ctx, "entity-1")
	assert.Error(t, err)
}
