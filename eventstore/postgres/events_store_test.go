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
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// nolint
func TestPostgresEventsStore(t *testing.T) {
	t.Run("testNewEventsStore", func(t *testing.T) {
		config := &Config{
			testContainer.Host(),
			testContainer.Port(),
			testDatabase,
			testUser,
			testDatabasePassword,
			testContainer.Schema(),
		}

		estore := NewEventsStore(config)
		assert.NotNil(t, estore)
		var p interface{} = estore
		_, ok := p.(persistence.EventsStore)
		assert.True(t, ok)
	})
	t.Run("testConnect:happy path", func(t *testing.T) {
		ctx := context.TODO()
		config := &Config{
			DBHost:     testContainer.Host(),
			DBPort:     testContainer.Port(),
			DBName:     testDatabase,
			DBUser:     testUser,
			DBPassword: testDatabasePassword,
			DBSchema:   testContainer.Schema(),
		}

		store := NewEventsStore(config)
		assert.NotNil(t, store)
		err := store.Connect(ctx)
		assert.NoError(t, err)
		err = store.Disconnect(ctx)
		assert.NoError(t, err)
	})
	t.Run("testConnect:database does not exist", func(t *testing.T) {
		ctx := context.TODO()
		config := &Config{
			DBHost:     testContainer.Host(),
			DBPort:     testContainer.Port(),
			DBName:     "testDatabase",
			DBUser:     testUser,
			DBPassword: testDatabasePassword,
			DBSchema:   testContainer.Schema(),
		}

		store := NewEventsStore(config)
		assert.NotNil(t, store)
		err := store.Connect(ctx)
		assert.Error(t, err)
	})
	t.Run("testWriteAndReplayEvents", func(t *testing.T) {
		ctx := context.TODO()
		config := &Config{
			DBHost:     testContainer.Host(),
			DBPort:     testContainer.Port(),
			DBName:     testDatabase,
			DBUser:     testUser,
			DBPassword: testDatabasePassword,
			DBSchema:   testContainer.Schema(),
		}

		store := NewEventsStore(config)
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

		shardNumbers, err := store.ShardNumbers(ctx)
		require.NoError(t, err)
		require.Len(t, shardNumbers, 2)

		offset := int64(0)
		events, nextOffset, err := store.GetShardEvents(ctx, shard1, offset, max)
		assert.NoError(t, err)
		assert.EqualValues(t, e1.GetTimestamp(), nextOffset)
		assert.Len(t, events, 1)

		err = schemaUtil.DropTable(ctx)
		assert.NoError(t, err)

		err = store.Disconnect(ctx)
		assert.NoError(t, err)
	})
	t.Run("testGetLatestEvent", func(t *testing.T) {
		ctx := context.TODO()
		config := &Config{
			testContainer.Host(),
			testContainer.Port(),
			testDatabase,
			testUser,
			testDatabasePassword,
			testContainer.Schema(),
		}

		store := NewEventsStore(config)
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
		config := &Config{
			testContainer.Host(),
			testContainer.Port(),
			testDatabase,
			testUser,
			testDatabasePassword,
			testContainer.Schema(),
		}

		store := NewEventsStore(config)
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
	t.Run("testShardNumbers", func(t *testing.T) {
		ctx := context.TODO()
		config := &Config{
			testContainer.Host(),
			testContainer.Port(),
			testDatabase,
			testUser,
			testDatabasePassword,
			testContainer.Schema(),
		}

		store := NewEventsStore(config)
		assert.NotNil(t, store)
		err := store.Connect(ctx)
		require.NoError(t, err)

		db, err := dbHandle(ctx)
		require.NoError(t, err)

		schemaUtil := NewSchemaUtils(db)

		err = schemaUtil.CreateTable(ctx)
		require.NoError(t, err)

		shardNumbers, err := store.ShardNumbers(ctx)
		require.NoError(t, err)
		require.Empty(t, shardNumbers)

		err = schemaUtil.DropTable(ctx)
		assert.NoError(t, err)

		err = store.Disconnect(ctx)
		assert.NoError(t, err)
	})
}

func TestEventsStoreNotConnected(t *testing.T) {
	ctx := context.Background()
	db, _ := NewMockDB(t)
	store := NewTestEventsStore(db, false)

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

	t.Run("ShardNumbers", func(t *testing.T) {
		_, err := store.ShardNumbers(ctx)
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

	t.Run("Connect already connected is no-op", func(t *testing.T) {
		db, _ := NewMockDB(t)
		store := NewTestEventsStore(db, true)
		err := store.Connect(ctx)
		assert.NoError(t, err)
	})

	t.Run("Connect propagates db error", func(t *testing.T) {
		db, _ := NewMockDB(t)
		db.connectErr = errors.New("connection refused")
		store := NewTestEventsStore(db, false)
		err := store.Connect(ctx)
		require.Error(t, err)
		assert.False(t, store.connected.Load())
	})

	t.Run("Disconnect when not connected is no-op", func(t *testing.T) {
		db, _ := NewMockDB(t)
		store := NewTestEventsStore(db, false)
		err := store.Disconnect(ctx)
		assert.NoError(t, err)
	})

	t.Run("Disconnect propagates db error", func(t *testing.T) {
		db, _ := NewMockDB(t)
		db.disconnectErr = errors.New("disconnect failed")
		store := NewTestEventsStore(db, true)
		err := store.Disconnect(ctx)
		require.Error(t, err)
		assert.True(t, store.connected.Load())
	})

	t.Run("Ping when connected delegates to db", func(t *testing.T) {
		db, _ := NewMockDB(t)
		store := NewTestEventsStore(db, true)
		err := store.Ping(ctx)
		assert.NoError(t, err)
	})

	t.Run("Ping when connected propagates db error", func(t *testing.T) {
		db, _ := NewMockDB(t)
		db.pingErr = errors.New("ping failed")
		store := NewTestEventsStore(db, true)
		err := store.Ping(ctx)
		require.Error(t, err)
		assert.EqualError(t, err, "ping failed")
	})

	t.Run("Ping when not connected calls Connect", func(t *testing.T) {
		db, _ := NewMockDB(t)
		store := NewTestEventsStore(db, false)
		err := store.Ping(ctx)
		assert.NoError(t, err)
		assert.True(t, store.connected.Load())
	})

	t.Run("Ping when not connected propagates Connect error", func(t *testing.T) {
		db, _ := NewMockDB(t)
		db.connectErr = errors.New("connect failed")
		store := NewTestEventsStore(db, false)
		err := store.Ping(ctx)
		require.Error(t, err)
		assert.False(t, store.connected.Load())
	})
}

func TestWriteEventsUnit(t *testing.T) {
	ctx := context.Background()

	t.Run("empty events is no-op", func(t *testing.T) {
		db, _ := NewMockDB(t)
		store := NewTestEventsStore(db, true)
		err := store.WriteEvents(ctx, []*egopb.Event{})
		assert.NoError(t, err)
	})

	t.Run("BeginTx error", func(t *testing.T) {
		db, _ := NewMockDB(t)
		db.beginTxErr = errors.New("begin tx failed")
		store := NewTestEventsStore(db, true)

		err := store.WriteEvents(ctx, []*egopb.Event{NewTestEvent("p1", 1, 1)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to obtain a database transaction")
	})

	t.Run("tx Exec error with successful rollback", func(t *testing.T) {
		db, mock := NewMockDB(t)
		store := NewTestEventsStore(db, true)

		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		mock.ExpectExec("INSERT INTO events_store").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnError(errors.New("exec failed"))
		mock.ExpectRollback()

		err := store.WriteEvents(ctx, []*egopb.Event{NewTestEvent("p1", 1, 1)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to record events")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("tx Exec error with rollback failure", func(t *testing.T) {
		db, mock := NewMockDB(t)
		store := NewTestEventsStore(db, true)

		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		mock.ExpectExec("INSERT INTO events_store").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnError(errors.New("exec failed"))
		mock.ExpectRollback().WillReturnError(errors.New("rollback failed"))

		err := store.WriteEvents(ctx, []*egopb.Event{NewTestEvent("p1", 1, 1)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unable to rollback db transaction")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("tx Commit error", func(t *testing.T) {
		db, mock := NewMockDB(t)
		store := NewTestEventsStore(db, true)

		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		mock.ExpectExec("INSERT INTO events_store").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnResult(pgxmock.NewResult("INSERT", 1))
		mock.ExpectCommit().WillReturnError(errors.New("commit failed"))

		err := store.WriteEvents(ctx, []*egopb.Event{NewTestEvent("p1", 1, 1)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to record events")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestDeleteEventsUnit(t *testing.T) {
	ctx := context.Background()

	t.Run("BeginTx error", func(t *testing.T) {
		db, _ := NewMockDB(t)
		db.beginTxErr = errors.New("begin tx failed")
		store := NewTestEventsStore(db, true)

		err := store.DeleteEvents(ctx, "p1", 5)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to obtain a database transaction")
	})

	t.Run("tx Exec error with successful rollback", func(t *testing.T) {
		db, mock := NewMockDB(t)
		store := NewTestEventsStore(db, true)

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
		db, mock := NewMockDB(t)
		store := NewTestEventsStore(db, true)

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
		db, mock := NewMockDB(t)
		store := NewTestEventsStore(db, true)

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

func TestReplayEventsUnit(t *testing.T) {
	ctx := context.Background()

	t.Run("SelectAll error", func(t *testing.T) {
		db, _ := NewMockDB(t)
		db.selectAllErr = errors.New("select failed")
		store := NewTestEventsStore(db, true)

		_, err := store.ReplayEvents(ctx, "p1", 1, 10, 100)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to fetch the events from the database")
	})
}

func TestGetLatestEventUnit(t *testing.T) {
	ctx := context.Background()

	t.Run("Select error", func(t *testing.T) {
		db, _ := NewMockDB(t)
		db.selectErr = errors.New("select failed")
		store := NewTestEventsStore(db, true)

		_, err := store.GetLatestEvent(ctx, "p1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to fetch the latest event from the database")
	})

	t.Run("empty result returns nil", func(t *testing.T) {
		db, _ := NewMockDB(t)
		store := NewTestEventsStore(db, true)

		event, err := store.GetLatestEvent(ctx, "p1")
		assert.NoError(t, err)
		assert.Nil(t, event)
	})
}

func TestGetShardEventsUnit(t *testing.T) {
	ctx := context.Background()

	t.Run("SelectAll error", func(t *testing.T) {
		db, _ := NewMockDB(t)
		db.selectAllErr = errors.New("select failed")
		store := NewTestEventsStore(db, true)

		_, _, err := store.GetShardEvents(ctx, 1, 0, 100)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to fetch the events from the database")
	})

	t.Run("empty result returns nil", func(t *testing.T) {
		db, _ := NewMockDB(t)
		store := NewTestEventsStore(db, true)

		events, offset, err := store.GetShardEvents(ctx, 1, 0, 100)
		assert.NoError(t, err)
		assert.Nil(t, events)
		assert.EqualValues(t, 0, offset)
	})
}

func TestShardNumbersUnit(t *testing.T) {
	ctx := context.Background()

	t.Run("SelectAll error", func(t *testing.T) {
		db, _ := NewMockDB(t)
		db.selectAllErr = errors.New("select failed")
		store := NewTestEventsStore(db, true)

		_, err := store.ShardNumbers(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to fetch the events from the database")
	})
}

func TestPersistenceIDsUnit(t *testing.T) {
	ctx := context.Background()

	t.Run("SelectAll error", func(t *testing.T) {
		db, _ := NewMockDB(t)
		db.selectAllErr = errors.New("select failed")
		store := NewTestEventsStore(db, true)

		_, _, err := store.PersistenceIDs(ctx, 10, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to fetch the events from the database")
	})

	t.Run("empty result returns nil", func(t *testing.T) {
		db, _ := NewMockDB(t)
		store := NewTestEventsStore(db, true)

		ids, token, err := store.PersistenceIDs(ctx, 10, "")
		assert.NoError(t, err)
		assert.Nil(t, ids)
		assert.Empty(t, token)
	})
}
