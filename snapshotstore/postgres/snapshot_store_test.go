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
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/ego/v4/egopb"
	"github.com/tochemey/ego/v4/persistence"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

var testContainer *TestContainer

const (
	testUser             = "test"
	testDatabase         = "testdb"
	testDatabasePassword = "test"
)

// TestMain will spawn a postgres database container that will be used for all tests
// making use of the postgres database container
func TestMain(m *testing.M) {
	// set the test container
	testContainer = NewTestContainer(testDatabase, testUser, testDatabasePassword)
	// execute the tests
	code := m.Run()
	// free resources
	testContainer.Cleanup()
	// exit the tests
	os.Exit(code)
}

// dbHandle returns a test db
func dbHandle(ctx context.Context) (*TestDB, error) {
	db := testContainer.GetTestDB()
	if err := db.Connect(ctx); err != nil {
		return nil, err
	}
	return db, nil
}

func TestPostgresSnapshotStore(t *testing.T) {
	t.Run("testNewSnapshotStore", func(t *testing.T) {
		config := &Config{
			DBHost:     testContainer.Host(),
			DBPort:     testContainer.Port(),
			DBName:     testDatabase,
			DBUser:     testUser,
			DBPassword: testDatabasePassword,
			DBSchema:   testContainer.Schema(),
		}

		store := NewSnapshotStore(config)
		assert.NotNil(t, store)
		var p interface{} = store
		_, ok := p.(persistence.SnapshotStore)
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

		store := NewSnapshotStore(config)
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
			DBName:     "nonexistent",
			DBUser:     testUser,
			DBPassword: testDatabasePassword,
			DBSchema:   testContainer.Schema(),
		}

		store := NewSnapshotStore(config)
		assert.NotNil(t, store)
		err := store.Connect(ctx)
		assert.Error(t, err)
	})
	t.Run("testWriteAndGetLatestSnapshot", func(t *testing.T) {
		ctx := context.TODO()
		config := &Config{
			DBHost:     testContainer.Host(),
			DBPort:     testContainer.Port(),
			DBName:     testDatabase,
			DBUser:     testUser,
			DBPassword: testDatabasePassword,
			DBSchema:   testContainer.Schema(),
		}

		db, err := dbHandle(ctx)
		require.NoError(t, err)
		schemaUtil := NewSchemaUtils(db)
		err = schemaUtil.CreateTable(ctx)
		require.NoError(t, err)

		store := NewSnapshotStore(config)
		assert.NotNil(t, store)
		err = store.Connect(ctx)
		require.NoError(t, err)

		// create a state payload
		state, err := anypb.New(wrapperspb.String("test-state"))
		require.NoError(t, err)

		persistenceID := "entity-1"
		ts := time.Now().UnixMilli()

		// write snapshot at sequence 5
		snapshot1 := &egopb.Snapshot{
			PersistenceId:  persistenceID,
			SequenceNumber: 5,
			State:          state,
			Timestamp:      ts,
		}
		assert.NoError(t, store.WriteSnapshot(ctx, snapshot1))

		// write snapshot at sequence 10
		snapshot2 := &egopb.Snapshot{
			PersistenceId:  persistenceID,
			SequenceNumber: 10,
			State:          state,
			Timestamp:      ts,
		}
		assert.NoError(t, store.WriteSnapshot(ctx, snapshot2))

		// get the latest snapshot - should be sequence 10
		latest, err := store.GetLatestSnapshot(ctx, persistenceID)
		require.NoError(t, err)
		require.NotNil(t, latest)
		assert.Equal(t, uint64(10), latest.GetSequenceNumber())
		assert.Equal(t, persistenceID, latest.GetPersistenceId())
		assert.True(t, proto.Equal(state, latest.GetState()))

		// get snapshot for non-existent entity - should return nil
		notFound, err := store.GetLatestSnapshot(ctx, "non-existent")
		require.NoError(t, err)
		assert.Nil(t, notFound)

		err = schemaUtil.DropTable(ctx)
		assert.NoError(t, err)

		err = store.Disconnect(ctx)
		assert.NoError(t, err)
	})
	t.Run("testDeleteSnapshots", func(t *testing.T) {
		ctx := context.TODO()
		config := &Config{
			DBHost:     testContainer.Host(),
			DBPort:     testContainer.Port(),
			DBName:     testDatabase,
			DBUser:     testUser,
			DBPassword: testDatabasePassword,
			DBSchema:   testContainer.Schema(),
		}

		db, err := dbHandle(ctx)
		require.NoError(t, err)
		schemaUtil := NewSchemaUtils(db)
		err = schemaUtil.CreateTable(ctx)
		require.NoError(t, err)

		store := NewSnapshotStore(config)
		assert.NotNil(t, store)
		err = store.Connect(ctx)
		require.NoError(t, err)

		// create a state payload
		state, err := anypb.New(wrapperspb.String("test-state"))
		require.NoError(t, err)

		persistenceID := "entity-2"
		ts := time.Now().UnixMilli()

		// write snapshots at sequences 5, 10, 15, 20
		for _, seq := range []uint64{5, 10, 15, 20} {
			snapshot := &egopb.Snapshot{
				PersistenceId:  persistenceID,
				SequenceNumber: seq,
				State:          state,
				Timestamp:      ts,
			}
			require.NoError(t, store.WriteSnapshot(ctx, snapshot))
		}

		// delete snapshots up to sequence 10 (inclusive)
		require.NoError(t, store.DeleteSnapshots(ctx, persistenceID, 10))

		// the latest snapshot should be at sequence 20
		latest, err := store.GetLatestSnapshot(ctx, persistenceID)
		require.NoError(t, err)
		require.NotNil(t, latest)
		assert.Equal(t, uint64(20), latest.GetSequenceNumber())

		// verify count - should have 2 remaining (15, 20)
		count, err := db.Count(ctx, tableName)
		require.NoError(t, err)
		assert.Equal(t, 2, count)

		err = schemaUtil.DropTable(ctx)
		assert.NoError(t, err)

		err = store.Disconnect(ctx)
		assert.NoError(t, err)
	})
	t.Run("testWriteSnapshotUpsert", func(t *testing.T) {
		ctx := context.TODO()
		config := &Config{
			DBHost:     testContainer.Host(),
			DBPort:     testContainer.Port(),
			DBName:     testDatabase,
			DBUser:     testUser,
			DBPassword: testDatabasePassword,
			DBSchema:   testContainer.Schema(),
		}

		db, err := dbHandle(ctx)
		require.NoError(t, err)
		schemaUtil := NewSchemaUtils(db)
		err = schemaUtil.CreateTable(ctx)
		require.NoError(t, err)

		store := NewSnapshotStore(config)
		assert.NotNil(t, store)
		err = store.Connect(ctx)
		require.NoError(t, err)

		persistenceID := "entity-3"
		ts := time.Now().UnixMilli()

		// write snapshot at sequence 5
		state1, err := anypb.New(wrapperspb.String("state-v1"))
		require.NoError(t, err)
		snapshot1 := &egopb.Snapshot{
			PersistenceId:  persistenceID,
			SequenceNumber: 5,
			State:          state1,
			Timestamp:      ts,
		}
		assert.NoError(t, store.WriteSnapshot(ctx, snapshot1))

		// overwrite snapshot at same sequence 5 with new state
		state2, err := anypb.New(wrapperspb.String("state-v2"))
		require.NoError(t, err)
		snapshot2 := &egopb.Snapshot{
			PersistenceId:  persistenceID,
			SequenceNumber: 5,
			State:          state2,
			Timestamp:      ts + 1000,
		}
		assert.NoError(t, store.WriteSnapshot(ctx, snapshot2))

		// should only have 1 row, with updated state
		count, err := db.Count(ctx, tableName)
		require.NoError(t, err)
		assert.Equal(t, 1, count)

		latest, err := store.GetLatestSnapshot(ctx, persistenceID)
		require.NoError(t, err)
		require.NotNil(t, latest)
		assert.True(t, proto.Equal(state2, latest.GetState()))
		assert.Equal(t, ts+1000, latest.GetTimestamp())

		err = schemaUtil.DropTable(ctx)
		assert.NoError(t, err)

		err = store.Disconnect(ctx)
		assert.NoError(t, err)
	})
}
