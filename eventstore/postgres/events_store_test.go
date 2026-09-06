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

	"github.com/jackc/pgx/v5"
	pgxmock "github.com/pashagolub/pgxmock/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/ego/v4/egopb"
	"github.com/tochemey/ego/v4/persistence"
	"github.com/tochemey/ego/v4/test/data/testpb"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// nolint
func TestPostgresEventsStore(t *testing.T) {
	t.Run("testNewEventsStore", func(t *testing.T) {
		estore := NewEventsStore(testConfig())
		assert.NotNil(t, estore)
		var p interface{} = estore
		_, ok := p.(persistence.EventsStore)
		assert.True(t, ok)
	})
	t.Run("testConnect:happy path", func(t *testing.T) {
		ctx := context.TODO()
		store := NewEventsStore(testConfig())
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

		store := NewEventsStore(config)
		assert.NotNil(t, store)
		err := store.Connect(ctx)
		assert.Error(t, err)
	})
	t.Run("testConnect:owned pool is rebuilt after Disconnect", func(t *testing.T) {
		ctx := context.TODO()
		store := NewEventsStore(testConfig())
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

		store := NewEventsStoreWithPool(pool)
		require.NoError(t, store.Connect(ctx))
		require.NoError(t, store.WriteEvents(ctx, []*egopb.Event{NewTestEvent("persistence-1", 1, 1)}))

		// disconnecting the store must leave the caller's pool open
		require.NoError(t, store.Disconnect(ctx))
		require.NoError(t, pool.Ping(ctx))

		// a second store on the same pool keeps working
		other := NewEventsStoreWithPool(pool)
		require.NoError(t, other.Connect(ctx))
		latest, err := other.GetLatestEvent(ctx, "persistence-1")
		require.NoError(t, err)
		require.NotNil(t, latest)
		assert.EqualValues(t, 1, latest.GetSequenceNumber())
		require.NoError(t, other.Disconnect(ctx))

		require.NoError(t, schemaUtil.DropTable(ctx))
	})
	t.Run("testWriteAndReplayEvents", func(t *testing.T) {
		ctx := context.TODO()
		store := NewEventsStore(testConfig())
		assert.NotNil(t, store)
		err := store.Connect(ctx)
		require.NoError(t, err)

		db, err := dbHandle(ctx)
		require.NoError(t, err)

		schemaUtil := NewSchemaUtils(db)

		err = schemaUtil.CreateTable(ctx)
		require.NoError(t, err)

		event, err := anypb.New(&testpb.AccountCreated{})
		assert.NoError(t, err)

		ts1 := timestamppb.Now()
		ts2 := timestamppb.Now()
		shard1 := uint64(5)
		shard2 := uint64(4)

		e1 := &egopb.Event{
			PersistenceId:  "persistence-1",
			SequenceNumber: 1,
			IsDeleted:      false,
			Event:          event,
			Timestamp:      ts1.AsTime().Unix(),
			Shard:          shard1,
		}

		event, err = anypb.New(&testpb.AccountCredited{})
		assert.NoError(t, err)

		e2 := &egopb.Event{
			PersistenceId:  "persistence-1",
			SequenceNumber: 2,
			IsDeleted:      false,
			Event:          event,
			Timestamp:      ts2.AsTime().Unix(),
			Shard:          shard2,
		}

		events := []*egopb.Event{e1, e2}
		err = store.WriteEvents(ctx, events)
		assert.NoError(t, err)

		persistenceID := "persistence-1"
		max := uint64(4)
		from := uint64(1)
		to := uint64(2)
		replayed, err := store.ReplayEvents(ctx, persistenceID, from, to, max)
		assert.NoError(t, err)
		assert.NotEmpty(t, replayed)
		assert.Len(t, replayed, 2)
		assert.Equal(t, prototext.Format(events[0]), prototext.Format(replayed[0]))
		assert.Equal(t, prototext.Format(events[1]), prototext.Format(replayed[1]))

		shardOffsets, err := store.ShardOffsets(ctx)
		require.NoError(t, err)
		require.Len(t, shardOffsets, 2)
		assert.Equal(t, e1.GetTimestamp(), shardOffsets[shard1])
		assert.Equal(t, e2.GetTimestamp(), shardOffsets[shard2])

		offset := int64(0)
		events, nextOffset, err := store.GetShardEvents(ctx, shard1, offset, max)
		assert.NoError(t, err)
		assert.EqualValues(t, e1.GetTimestamp(), nextOffset)
		assert.Len(t, events, 1)

		ids, nextToken, err := store.PersistenceIDs(ctx, 10, "")
		require.NoError(t, err)
		assert.Equal(t, []string{persistenceID}, ids)
		assert.Equal(t, persistenceID, nextToken)

		err = schemaUtil.DropTable(ctx)
		assert.NoError(t, err)

		err = store.Disconnect(ctx)
		assert.NoError(t, err)
	})
	t.Run("testGetLatestEvent", func(t *testing.T) {
		ctx := context.TODO()
		store := NewEventsStore(testConfig())
		assert.NotNil(t, store)
		err := store.Connect(ctx)
		require.NoError(t, err)

		db, err := dbHandle(ctx)
		require.NoError(t, err)

		schemaUtil := NewSchemaUtils(db)

		err = schemaUtil.CreateTable(ctx)
		require.NoError(t, err)

		event, err := anypb.New(&testpb.AccountCreated{})
		assert.NoError(t, err)

		ts1 := timestamppb.New(time.Now().UTC())
		ts2 := timestamppb.New(time.Now().UTC())
		shard1 := uint64(7)
		shard2 := uint64(4)

		e1 := &egopb.Event{
			PersistenceId:  "persistence-1",
			SequenceNumber: 1,
			IsDeleted:      false,
			Event:          event,
			Timestamp:      ts1.AsTime().Unix(),
			Shard:          shard1,
		}

		event, err = anypb.New(&testpb.AccountCredited{})
		assert.NoError(t, err)

		e2 := &egopb.Event{
			PersistenceId:  "persistence-1",
			SequenceNumber: 2,
			IsDeleted:      false,
			Event:          event,
			Timestamp:      ts2.AsTime().Unix(),
			Shard:          shard2,
		}

		events := []*egopb.Event{e1, e2}
		err = store.WriteEvents(ctx, events)
		assert.NoError(t, err)

		persistenceID := "persistence-1"

		actual, err := store.GetLatestEvent(ctx, persistenceID)
		assert.NoError(t, err)
		assert.NotNil(t, actual)

		assert.Equal(t, prototext.Format(e2), prototext.Format(actual))

		err = schemaUtil.DropTable(ctx)
		assert.NoError(t, err)

		err = store.Disconnect(ctx)
		assert.NoError(t, err)
	})
	t.Run("testDeleteEvents", func(t *testing.T) {
		ctx := context.TODO()
		store := NewEventsStore(testConfig())
		assert.NotNil(t, store)
		err := store.Connect(ctx)
		require.NoError(t, err)

		db, err := dbHandle(ctx)
		require.NoError(t, err)

		schemaUtil := NewSchemaUtils(db)

		err = schemaUtil.CreateTable(ctx)
		require.NoError(t, err)

		event, err := anypb.New(&testpb.AccountCreated{})
		assert.NoError(t, err)

		ts1 := timestamppb.New(time.Now().UTC())
		ts2 := timestamppb.New(time.Now().UTC())
		shard1 := uint64(9)
		shard2 := uint64(8)

		e1 := &egopb.Event{
			PersistenceId:  "persistence-1",
			SequenceNumber: 1,
			IsDeleted:      false,
			Event:          event,
			Timestamp:      ts1.AsTime().Unix(),
			Shard:          shard1,
		}

		event, err = anypb.New(&testpb.AccountCredited{})
		assert.NoError(t, err)

		e2 := &egopb.Event{
			PersistenceId:  "persistence-1",
			SequenceNumber: 2,
			IsDeleted:      false,
			Event:          event,
			Timestamp:      ts2.AsTime().Unix(),
			Shard:          shard2,
		}

		events := []*egopb.Event{e1, e2}
		err = store.WriteEvents(ctx, events)
		assert.NoError(t, err)

		persistenceID := "persistence-1"

		actual, err := store.GetLatestEvent(ctx, persistenceID)
		assert.NoError(t, err)
		assert.NotNil(t, actual)

		assert.Equal(t, prototext.Format(e2), prototext.Format(actual))

		// let us delete the events
		err = store.DeleteEvents(ctx, persistenceID, uint64(3))
		assert.NoError(t, err)
		actual, err = store.GetLatestEvent(ctx, persistenceID)
		assert.NoError(t, err)
		assert.Nil(t, actual)

		err = schemaUtil.DropTable(ctx)
		assert.NoError(t, err)

		err = store.Disconnect(ctx)
		assert.NoError(t, err)
	})
	t.Run("testShardOffsets", func(t *testing.T) {
		ctx := context.TODO()
		store := NewEventsStore(testConfig())
		assert.NotNil(t, store)
		err := store.Connect(ctx)
		require.NoError(t, err)

		db, err := dbHandle(ctx)
		require.NoError(t, err)

		schemaUtil := NewSchemaUtils(db)

		err = schemaUtil.CreateTable(ctx)
		require.NoError(t, err)

		// an empty journal yields an empty map
		shardOffsets, err := store.ShardOffsets(ctx)
		require.NoError(t, err)
		require.NotNil(t, shardOffsets)
		require.Empty(t, shardOffsets)

		// every shard maps to the timestamp of its most recent event
		e1 := NewTestEvent("persistence-1", 1, 1)
		e1.Timestamp = 100
		e2 := NewTestEvent("persistence-1", 2, 1)
		e2.Timestamp = 300
		e3 := NewTestEvent("persistence-2", 1, 2)
		e3.Timestamp = 200
		require.NoError(t, store.WriteEvents(ctx, []*egopb.Event{e1, e2, e3}))

		shardOffsets, err = store.ShardOffsets(ctx)
		require.NoError(t, err)
		assert.Equal(t, map[uint64]int64{1: 300, 2: 200}, shardOffsets)

		err = schemaUtil.DropTable(ctx)
		assert.NoError(t, err)

		err = store.Disconnect(ctx)
		assert.NoError(t, err)
	})
}

// anyArgs returns n pgxmock argument matchers accepting any value
func anyArgs(n int) []any {
	args := make([]any, n)
	for i := range args {
		args[i] = pgxmock.AnyArg()
	}
	return args
}

// newMockStore creates an events store backed by a pgxmock pool
func newMockStore(t *testing.T) (*EventsStore, pgxmock.PgxPoolIface) {
	t.Helper()
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	t.Cleanup(mock.Close)
	return NewEventsStoreWithPool(mock), mock
}

// newConnectedMockStore creates an events store backed by a pgxmock pool and connects it
func newConnectedMockStore(t *testing.T) (*EventsStore, pgxmock.PgxPoolIface) {
	t.Helper()
	store, mock := newMockStore(t)
	mock.ExpectPing()
	require.NoError(t, store.Connect(context.Background()))
	return store, mock
}

func TestEventsStoreNotConnected(t *testing.T) {
	ctx := context.Background()
	store, _ := newMockStore(t)

	t.Run("WriteEvents", func(t *testing.T) {
		err := store.WriteEvents(ctx, []*egopb.Event{NewTestEvent("p1", 1, 1)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not connected")
	})

	t.Run("DeleteEvents", func(t *testing.T) {
		err := store.DeleteEvents(ctx, "p1", 1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not connected")
	})

	t.Run("ReplayEvents", func(t *testing.T) {
		_, err := store.ReplayEvents(ctx, "p1", 1, 10, 100)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not connected")
	})

	t.Run("GetLatestEvent", func(t *testing.T) {
		_, err := store.GetLatestEvent(ctx, "p1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not connected")
	})

	t.Run("GetShardEvents", func(t *testing.T) {
		_, _, err := store.GetShardEvents(ctx, 1, 0, 100)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not connected")
	})

	t.Run("ShardOffsets", func(t *testing.T) {
		_, err := store.ShardOffsets(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not connected")
	})

	t.Run("PersistenceIDs", func(t *testing.T) {
		_, _, err := store.PersistenceIDs(ctx, 10, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not connected")
	})
}

func TestEventsStoreConnectionLifecycle(t *testing.T) {
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
		store := NewEventsStoreWithPool(nil)
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

func TestWriteEventsUnit(t *testing.T) {
	ctx := context.Background()

	t.Run("empty events is no-op", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)
		assert.NoError(t, store.WriteEvents(ctx, []*egopb.Event{}))
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("BeginTx error", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)
		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.ReadCommitted}).WillReturnError(errors.New("begin tx failed"))

		err := store.WriteEvents(ctx, []*egopb.Event{NewTestEvent("p1", 1, 1)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to obtain a database transaction")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("tx Exec error with successful rollback", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)

		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		mock.ExpectExec("INSERT INTO events_store").
			WithArgs(anyArgs(9)...).
			WillReturnError(errors.New("exec failed"))
		mock.ExpectRollback()

		err := store.WriteEvents(ctx, []*egopb.Event{NewTestEvent("p1", 1, 1)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to record events")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("tx Exec error with rollback failure", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)

		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		mock.ExpectExec("INSERT INTO events_store").
			WithArgs(anyArgs(9)...).
			WillReturnError(errors.New("exec failed"))
		mock.ExpectRollback().WillReturnError(errors.New("rollback failed"))

		err := store.WriteEvents(ctx, []*egopb.Event{NewTestEvent("p1", 1, 1)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unable to rollback db transaction")
		assert.Contains(t, err.Error(), "exec failed")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("tx Commit error", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)

		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		mock.ExpectExec("INSERT INTO events_store").
			WithArgs(anyArgs(9)...).
			WillReturnResult(pgxmock.NewResult("INSERT", 1))
		mock.ExpectCommit().WillReturnError(errors.New("commit failed"))

		err := store.WriteEvents(ctx, []*egopb.Event{NewTestEvent("p1", 1, 1)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to record events")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("events are inserted in batches", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)
		store.insertBatchSize = 2

		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		// three events with a batch size of two yield two insert statements
		mock.ExpectExec("INSERT INTO events_store").WithArgs(anyArgs(18)...).WillReturnResult(pgxmock.NewResult("INSERT", 2))
		mock.ExpectExec("INSERT INTO events_store").WithArgs(anyArgs(9)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
		mock.ExpectCommit()

		events := []*egopb.Event{NewTestEvent("p1", 1, 1), NewTestEvent("p1", 2, 1), NewTestEvent("p1", 3, 1)}
		require.NoError(t, store.WriteEvents(ctx, events))
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestDeleteEventsUnit(t *testing.T) {
	ctx := context.Background()

	t.Run("BeginTx error", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)
		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.ReadCommitted}).WillReturnError(errors.New("begin tx failed"))

		err := store.DeleteEvents(ctx, "p1", 5)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to obtain a database transaction")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("tx Exec error with successful rollback", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)

		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		mock.ExpectExec("DELETE FROM events_store").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnError(errors.New("exec failed"))
		mock.ExpectRollback()

		err := store.DeleteEvents(ctx, "p1", 5)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to delete events from the database")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("tx Exec error with rollback failure", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)

		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		mock.ExpectExec("DELETE FROM events_store").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnError(errors.New("exec failed"))
		mock.ExpectRollback().WillReturnError(errors.New("rollback failed"))

		err := store.DeleteEvents(ctx, "p1", 5)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unable to rollback db transaction")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("tx Commit error", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)

		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		mock.ExpectExec("DELETE FROM events_store").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnResult(pgxmock.NewResult("DELETE", 2))
		mock.ExpectCommit().WillReturnError(errors.New("commit failed"))

		err := store.DeleteEvents(ctx, "p1", 5)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to commit delete events")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// eventRows builds mocked rows for the given events, mirroring the events_store columns
func eventRows(t *testing.T, events ...*egopb.Event) *pgxmock.Rows {
	t.Helper()
	rows := pgxmock.NewRows(columns)
	for _, event := range events {
		payload, err := proto.Marshal(event.GetEvent())
		require.NoError(t, err)
		manifest := string(event.GetEvent().ProtoReflect().Descriptor().FullName())
		rows.AddRow(
			event.GetPersistenceId(),
			event.GetSequenceNumber(),
			event.GetIsDeleted(),
			payload,
			manifest,
			event.GetTimestamp(),
			event.GetShard(),
			event.GetEncryptionKeyId(),
			event.GetIsEncrypted(),
		)
	}
	return rows
}

func TestReplayEventsUnit(t *testing.T) {
	ctx := context.Background()

	t.Run("Query error", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)
		mock.ExpectQuery("FROM events_store WHERE persistence_id").WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).WillReturnError(errors.New("select failed"))

		_, err := store.ReplayEvents(ctx, "p1", 1, 10, 100)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to fetch the events from the database")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("rows are scanned into events", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)
		e1 := NewTestEvent("p1", 1, 3)
		e2 := NewTestEvent("p1", 2, 3)
		mock.ExpectQuery("FROM events_store WHERE persistence_id").WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).WillReturnRows(eventRows(t, e1, e2))

		events, err := store.ReplayEvents(ctx, "p1", 1, 10, 100)
		require.NoError(t, err)
		require.Len(t, events, 2)
		assert.True(t, proto.Equal(e1, events[0]))
		assert.True(t, proto.Equal(e2, events[1]))
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestGetLatestEventUnit(t *testing.T) {
	ctx := context.Background()

	t.Run("Query error", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)
		mock.ExpectQuery("ORDER BY sequence_number DESC").WithArgs(pgxmock.AnyArg()).WillReturnError(errors.New("select failed"))

		_, err := store.GetLatestEvent(ctx, "p1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to fetch the latest event from the database")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty result returns nil", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)
		mock.ExpectQuery("ORDER BY sequence_number DESC").WithArgs(pgxmock.AnyArg()).WillReturnRows(pgxmock.NewRows(columns))

		event, err := store.GetLatestEvent(ctx, "p1")
		assert.NoError(t, err)
		assert.Nil(t, event)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("row is scanned into an event", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)
		expected := NewTestEvent("p1", 7, 3)
		mock.ExpectQuery("ORDER BY sequence_number DESC").WithArgs(pgxmock.AnyArg()).WillReturnRows(eventRows(t, expected))

		event, err := store.GetLatestEvent(ctx, "p1")
		require.NoError(t, err)
		assert.True(t, proto.Equal(expected, event))
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestGetShardEventsUnit(t *testing.T) {
	ctx := context.Background()

	t.Run("Query error", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)
		mock.ExpectQuery("WHERE shard_number").WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).WillReturnError(errors.New("select failed"))

		_, _, err := store.GetShardEvents(ctx, 1, 0, 100)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to fetch the events from the database")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty result returns nil", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)
		mock.ExpectQuery("WHERE shard_number").WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).WillReturnRows(pgxmock.NewRows(columns))

		events, offset, err := store.GetShardEvents(ctx, 1, 0, 100)
		assert.NoError(t, err)
		assert.Nil(t, events)
		assert.EqualValues(t, 0, offset)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("next offset is the timestamp of the last event", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)
		e1 := NewTestEvent("p1", 1, 1)
		e1.Timestamp = 100
		e2 := NewTestEvent("p2", 1, 1)
		e2.Timestamp = 250
		mock.ExpectQuery("WHERE shard_number").WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).WillReturnRows(eventRows(t, e1, e2))

		events, offset, err := store.GetShardEvents(ctx, 1, 0, 100)
		require.NoError(t, err)
		assert.Len(t, events, 2)
		assert.EqualValues(t, 250, offset)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestShardOffsetsUnit(t *testing.T) {
	ctx := context.Background()

	t.Run("Query error", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)
		mock.ExpectQuery("GROUP BY shard_number").WillReturnError(errors.New("select failed"))

		_, err := store.ShardOffsets(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to fetch the shard offsets from the database")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty result returns an empty map", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)
		mock.ExpectQuery("GROUP BY shard_number").WillReturnRows(pgxmock.NewRows([]string{"shard_number", "current_offset"}))

		offsets, err := store.ShardOffsets(ctx)
		require.NoError(t, err)
		require.NotNil(t, offsets)
		assert.Empty(t, offsets)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("rows are scanned into the map", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)
		mock.ExpectQuery("GROUP BY shard_number").WillReturnRows(
			pgxmock.NewRows([]string{"shard_number", "current_offset"}).
				AddRow(uint64(1), int64(300)).
				AddRow(uint64(2), int64(200)),
		)

		offsets, err := store.ShardOffsets(ctx)
		require.NoError(t, err)
		assert.Equal(t, map[uint64]int64{1: 300, 2: 200}, offsets)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestPersistenceIDsUnit(t *testing.T) {
	ctx := context.Background()

	t.Run("Query error", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)
		mock.ExpectQuery("SELECT DISTINCT persistence_id FROM events_store").WillReturnError(errors.New("select failed"))

		_, _, err := store.PersistenceIDs(ctx, 10, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to fetch the events from the database")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty result returns nil", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)
		mock.ExpectQuery("SELECT DISTINCT persistence_id FROM events_store").WillReturnRows(pgxmock.NewRows([]string{"persistence_id"}))

		ids, next, err := store.PersistenceIDs(ctx, 10, "")
		assert.NoError(t, err)
		assert.Nil(t, ids)
		assert.Empty(t, next)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("next page token is the last persistence id", func(t *testing.T) {
		store, mock := newConnectedMockStore(t)
		mock.ExpectQuery("SELECT DISTINCT persistence_id FROM events_store").
			WithArgs("p0").
			WillReturnRows(pgxmock.NewRows([]string{"persistence_id"}).AddRow("p1").AddRow("p2"))

		ids, next, err := store.PersistenceIDs(ctx, 2, "p0")
		require.NoError(t, err)
		assert.Equal(t, []string{"p1", "p2"}, ids)
		assert.Equal(t, "p2", next)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}
