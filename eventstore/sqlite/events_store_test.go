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
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/ego/v4/egopb"
	"github.com/tochemey/ego/v4/persistence"
	"github.com/tochemey/ego/v4/test/data/testpb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

func TestNewEventsStore(t *testing.T) {
	store := NewEventsStore(&Config{})
	require.NotNil(t, store)

	var iface any = store
	_, ok := iface.(persistence.EventsStore)
	assert.True(t, ok, "the store must implement persistence.EventsStore")
}

func TestConnectionLifecycle(t *testing.T) {
	ctx := context.Background()

	t.Run("an owned handle is opened by Connect and closed by Disconnect", func(t *testing.T) {
		store := NewEventsStore(testConfig(t))

		require.NoError(t, store.Connect(ctx))
		require.NoError(t, store.Ping(ctx))

		require.NoError(t, store.Disconnect(ctx))
		_, err := store.activeDB()
		assert.ErrorIs(t, err, errNotConnected)
	})

	t.Run("an owned handle is reopened on the next Connect", func(t *testing.T) {
		store := NewEventsStore(testConfig(t))

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
		store := NewEventsStore(testConfig(t))
		assert.NoError(t, store.Disconnect(ctx))
	})

	t.Run("Connect reports a database that cannot be opened", func(t *testing.T) {
		store := NewEventsStore(&Config{DBPath: t.TempDir() + "/missing-directory/events.db"})
		assert.Error(t, store.Connect(ctx))
	})

	t.Run("Ping connects when the store is not connected yet", func(t *testing.T) {
		store := NewEventsStore(testConfig(t))
		require.NoError(t, store.Ping(ctx))

		_, err := store.activeDB()
		assert.NoError(t, err)
		require.NoError(t, store.Disconnect(ctx))
	})

	t.Run("a supplied handle is pinged by Connect and left open by Disconnect", func(t *testing.T) {
		db := newTestDB(t)
		store := NewEventsStoreWithSqlite(db)

		require.NoError(t, store.Connect(ctx))
		require.NoError(t, store.WriteEvents(ctx, []*egopb.Event{NewTestEvent("persistence-1", 1, 1)}))
		require.NoError(t, store.Disconnect(ctx))

		// the handle is still usable after the store let go of it
		require.NoError(t, db.PingContext(ctx))
		assert.Equal(t, 1, countRows(t, db))

		// and a second store can run on it
		other := NewEventsStoreWithSqlite(db)
		require.NoError(t, other.Connect(ctx))
		latest, err := other.GetLatestEvent(ctx, "persistence-1")
		require.NoError(t, err)
		require.NotNil(t, latest)
		assert.EqualValues(t, 1, latest.GetSequenceNumber())
		require.NoError(t, other.Disconnect(ctx))
	})

	t.Run("Connect fails when no handle was supplied", func(t *testing.T) {
		store := NewEventsStoreWithSqlite(nil)
		err := store.Connect(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "database handle is not defined")
	})

	t.Run("Connect reports a handle that cannot be reached", func(t *testing.T) {
		db := newTestDB(t)
		require.NoError(t, db.Close())

		store := NewEventsStoreWithSqlite(db)
		err := store.Connect(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to ping the database")
	})
}

func TestOperationsRequireAConnection(t *testing.T) {
	ctx := context.Background()
	store := NewEventsStore(testConfig(t))

	t.Run("WriteEvents", func(t *testing.T) {
		assert.ErrorIs(t, store.WriteEvents(ctx, []*egopb.Event{NewTestEvent("p1", 1, 1)}), errNotConnected)
	})
	t.Run("DeleteEvents", func(t *testing.T) {
		assert.ErrorIs(t, store.DeleteEvents(ctx, "p1", 1), errNotConnected)
	})
	t.Run("ReplayEvents", func(t *testing.T) {
		_, err := store.ReplayEvents(ctx, "p1", 1, 10, 100)
		assert.ErrorIs(t, err, errNotConnected)
	})
	t.Run("GetLatestEvent", func(t *testing.T) {
		_, err := store.GetLatestEvent(ctx, "p1")
		assert.ErrorIs(t, err, errNotConnected)
	})
	t.Run("GetShardEvents", func(t *testing.T) {
		_, _, err := store.GetShardEvents(ctx, 1, 0, 100)
		assert.ErrorIs(t, err, errNotConnected)
	})
	t.Run("ShardOffsets", func(t *testing.T) {
		_, err := store.ShardOffsets(ctx)
		assert.ErrorIs(t, err, errNotConnected)
	})
	t.Run("PersistenceIDs", func(t *testing.T) {
		_, _, err := store.PersistenceIDs(ctx, 10, "")
		assert.ErrorIs(t, err, errNotConnected)
	})
}

func TestWriteAndReplayEvents(t *testing.T) {
	ctx := context.Background()
	store := newConnectedStore(t)

	created, err := anypb.New(&testpb.AccountCreated{})
	require.NoError(t, err)
	credited, err := anypb.New(&testpb.AccountCredited{})
	require.NoError(t, err)

	first := &egopb.Event{PersistenceId: "persistence-1", SequenceNumber: 1, Event: created, Timestamp: 100, Shard: 5}
	second := &egopb.Event{PersistenceId: "persistence-1", SequenceNumber: 2, Event: credited, Timestamp: 200, Shard: 4}
	require.NoError(t, store.WriteEvents(ctx, []*egopb.Event{first, second}))

	t.Run("replay returns the events in sequence order", func(t *testing.T) {
		replayed, err := store.ReplayEvents(ctx, "persistence-1", 1, 2, 10)
		require.NoError(t, err)
		require.Len(t, replayed, 2)
		assert.True(t, proto.Equal(first, replayed[0]))
		assert.True(t, proto.Equal(second, replayed[1]))
	})

	t.Run("replay honours the sequence bounds", func(t *testing.T) {
		replayed, err := store.ReplayEvents(ctx, "persistence-1", 2, 2, 10)
		require.NoError(t, err)
		require.Len(t, replayed, 1)
		assert.True(t, proto.Equal(second, replayed[0]))
	})

	t.Run("replay of an unknown entity is empty", func(t *testing.T) {
		replayed, err := store.ReplayEvents(ctx, "missing", 1, 10, 10)
		require.NoError(t, err)
		assert.Empty(t, replayed)
	})

	t.Run("the latest event is the one with the highest sequence number", func(t *testing.T) {
		latest, err := store.GetLatestEvent(ctx, "persistence-1")
		require.NoError(t, err)
		assert.True(t, proto.Equal(second, latest))
	})

	t.Run("the latest event of an unknown entity is nil", func(t *testing.T) {
		latest, err := store.GetLatestEvent(ctx, "missing")
		require.NoError(t, err)
		assert.Nil(t, latest)
	})

	t.Run("persistence ids are listed and paged", func(t *testing.T) {
		require.NoError(t, store.WriteEvents(ctx, []*egopb.Event{NewTestEvent("persistence-2", 1, 5)}))

		ids, next, err := store.PersistenceIDs(ctx, 1, "")
		require.NoError(t, err)
		assert.Equal(t, []string{"persistence-1"}, ids)
		assert.Equal(t, "persistence-1", next)

		ids, next, err = store.PersistenceIDs(ctx, 10, next)
		require.NoError(t, err)
		assert.Equal(t, []string{"persistence-2"}, ids)
		assert.Equal(t, "persistence-2", next)

		ids, next, err = store.PersistenceIDs(ctx, 10, next)
		require.NoError(t, err)
		assert.Empty(t, ids)
		assert.Empty(t, next)
	})
}

func TestWriteEventsIsAtomic(t *testing.T) {
	ctx := context.Background()
	store := newConnectedStore(t)

	t.Run("an empty batch writes nothing", func(t *testing.T) {
		require.NoError(t, store.WriteEvents(ctx, nil))
		db, err := store.activeDB()
		require.NoError(t, err)
		assert.Equal(t, 0, countRows(t, db))
	})

	t.Run("a batch that violates the primary key leaves no row behind", func(t *testing.T) {
		duplicate := []*egopb.Event{
			NewTestEvent("persistence-1", 1, 1),
			NewTestEvent("persistence-1", 1, 1),
		}
		require.Error(t, store.WriteEvents(ctx, duplicate))

		db, err := store.activeDB()
		require.NoError(t, err)
		assert.Equal(t, 0, countRows(t, db), "the failed transaction must have been rolled back")
	})

	t.Run("a batch larger than the batch size is written in full", func(t *testing.T) {
		store.insertBatchSize = 10

		events := make([]*egopb.Event, 0, 25)
		for i := range 25 {
			events = append(events, NewTestEvent("persistence-batch", uint64(i+1), 1))
		}
		require.NoError(t, store.WriteEvents(ctx, events))

		db, err := store.activeDB()
		require.NoError(t, err)
		assert.Equal(t, 25, countRows(t, db))
	})
}

func TestDeleteEvents(t *testing.T) {
	ctx := context.Background()
	store := newConnectedStore(t)

	events := make([]*egopb.Event, 0, 5)
	for i := range 5 {
		events = append(events, NewTestEvent("persistence-1", uint64(i+1), 1))
	}
	require.NoError(t, store.WriteEvents(ctx, events))

	t.Run("events up to the sequence number are removed", func(t *testing.T) {
		require.NoError(t, store.DeleteEvents(ctx, "persistence-1", 3))

		latest, err := store.GetLatestEvent(ctx, "persistence-1")
		require.NoError(t, err)
		assert.EqualValues(t, 5, latest.GetSequenceNumber())

		db, err := store.activeDB()
		require.NoError(t, err)
		assert.Equal(t, 2, countRows(t, db))
	})

	t.Run("deleting every event leaves no latest event", func(t *testing.T) {
		require.NoError(t, store.DeleteEvents(ctx, "persistence-1", 5))

		latest, err := store.GetLatestEvent(ctx, "persistence-1")
		require.NoError(t, err)
		assert.Nil(t, latest)
	})
}

func TestShardQueries(t *testing.T) {
	ctx := context.Background()
	store := newConnectedStore(t)

	t.Run("an empty journal has no shard offset", func(t *testing.T) {
		offsets, err := store.ShardOffsets(ctx)
		require.NoError(t, err)
		require.NotNil(t, offsets)
		assert.Empty(t, offsets)
	})

	first := NewTestEvent("persistence-1", 1, 1)
	first.Timestamp = 100
	second := NewTestEvent("persistence-1", 2, 1)
	second.Timestamp = 300
	third := NewTestEvent("persistence-2", 1, 2)
	third.Timestamp = 200
	require.NoError(t, store.WriteEvents(ctx, []*egopb.Event{first, second, third}))

	t.Run("every shard maps to the timestamp of its latest event", func(t *testing.T) {
		offsets, err := store.ShardOffsets(ctx)
		require.NoError(t, err)
		assert.Equal(t, map[uint64]int64{1: 300, 2: 200}, offsets)
	})

	t.Run("a shard is read forward from an offset", func(t *testing.T) {
		events, next, err := store.GetShardEvents(ctx, 1, 0, 10)
		require.NoError(t, err)
		require.Len(t, events, 2)
		assert.EqualValues(t, 300, next)

		events, next, err = store.GetShardEvents(ctx, 1, 100, 10)
		require.NoError(t, err)
		require.Len(t, events, 1)
		assert.EqualValues(t, 300, next)
	})

	t.Run("a shard read past its last event is empty", func(t *testing.T) {
		events, next, err := store.GetShardEvents(ctx, 1, 300, 10)
		require.NoError(t, err)
		assert.Empty(t, events)
		assert.Zero(t, next)
	})

	t.Run("an unknown shard is empty", func(t *testing.T) {
		events, next, err := store.GetShardEvents(ctx, 99, 0, 10)
		require.NoError(t, err)
		assert.Empty(t, events)
		assert.Zero(t, next)
	})
}

func TestQueryErrorsAreReported(t *testing.T) {
	ctx := context.Background()
	store := newConnectedStore(t)

	db, err := store.activeDB()
	require.NoError(t, err)
	dropSchema(t, db)

	t.Run("WriteEvents", func(t *testing.T) {
		err := store.WriteEvents(ctx, []*egopb.Event{NewTestEvent("p1", 1, 1)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to record events")
	})
	t.Run("DeleteEvents", func(t *testing.T) {
		err := store.DeleteEvents(ctx, "p1", 1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to delete events from the database")
	})
	t.Run("ReplayEvents", func(t *testing.T) {
		_, err := store.ReplayEvents(ctx, "p1", 1, 10, 10)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to fetch the events from the database")
	})
	t.Run("GetLatestEvent", func(t *testing.T) {
		_, err := store.GetLatestEvent(ctx, "p1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to fetch the latest event from the database")
	})
	t.Run("GetShardEvents", func(t *testing.T) {
		_, _, err := store.GetShardEvents(ctx, 1, 0, 10)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to fetch the events from the database")
	})
	t.Run("ShardOffsets", func(t *testing.T) {
		_, err := store.ShardOffsets(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to fetch the shard offsets from the database")
	})
	t.Run("PersistenceIDs", func(t *testing.T) {
		_, _, err := store.PersistenceIDs(ctx, 10, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to fetch the events from the database")
	})
}

// failingDB refuses to start a transaction, which no real handle does on demand
type failingDB struct {
	Sqlite
	err error
}

func (f failingDB) BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error) { return nil, f.err }
func (f failingDB) PingContext(context.Context) error                        { return nil }

func TestTransactionErrorsAreReported(t *testing.T) {
	ctx := context.Background()
	db := failingDB{err: errors.New("no transaction for you")}

	store := NewEventsStoreWithSqlite(db)
	require.NoError(t, store.Connect(ctx))

	t.Run("WriteEvents", func(t *testing.T) {
		err := store.WriteEvents(ctx, []*egopb.Event{NewTestEvent("p1", 1, 1)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to obtain a database transaction")
	})

	t.Run("DeleteEvents", func(t *testing.T) {
		err := store.DeleteEvents(ctx, "p1", 1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to obtain a database transaction")
	})
}

func TestUnmarshallingFailureIsReported(t *testing.T) {
	ctx := context.Background()
	store := newConnectedStore(t)

	db, err := store.activeDB()
	require.NoError(t, err)

	// a manifest no protobuf registry can resolve
	_, err = db.ExecContext(ctx,
		"INSERT INTO "+tableName+" (persistence_id, sequence_number, is_deleted, event_payload, event_manifest, timestamp, shard_number, encryption_key_id, is_encrypted) VALUES (?,?,?,?,?,?,?,?,?)",
		"persistence-1", 1, false, []byte("payload"), "unknown.Message", 100, 1, "", false)
	require.NoError(t, err)

	_, err = store.GetLatestEvent(ctx, "persistence-1")
	assert.Error(t, err)

	_, err = store.ReplayEvents(ctx, "persistence-1", 1, 10, 10)
	assert.Error(t, err)
}
