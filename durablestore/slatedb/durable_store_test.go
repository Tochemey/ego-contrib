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

package slatedb

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/ego/v4/egopb"
	"github.com/tochemey/ego/v4/persistence"
	"github.com/tochemey/ego/v4/test/data/testpb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// nolint
func TestDurableStore(t *testing.T) {
	t.Run("testNew", func(t *testing.T) {
		store := NewDurableStore(&Config{ObjectStoreURL: "memory:///", DbPath: "test-durable-new"})
		assert.NotNil(t, store)
		var p any = store
		_, ok := p.(persistence.StateStore)
		assert.True(t, ok)
	})
	t.Run("testConnect", func(t *testing.T) {
		ctx := context.TODO()
		store := NewDurableStore(&Config{ObjectStoreURL: "memory:///", DbPath: "test-durable-connect"})
		assert.NotNil(t, store)
		require.NoError(t, store.Connect(ctx))
		require.NoError(t, store.Disconnect(ctx))
	})
	t.Run("testWriteAndGetLatestState", func(t *testing.T) {
		ctx := context.TODO()
		state, err := anypb.New(&testpb.Account{AccountId: "account-42", AccountBalance: 100.0})
		require.NoError(t, err)

		durableState := &egopb.DurableState{
			PersistenceId:  "persistence-1",
			VersionNumber:  1,
			ResultingState: state,
			Timestamp:      timestamppb.Now().AsTime().Unix(),
			Shard:          9,
		}

		store := NewDurableStore(&Config{ObjectStoreURL: "memory:///", DbPath: "test-durable-write"})
		require.NoError(t, store.Connect(ctx))

		require.NoError(t, store.WriteState(ctx, durableState))

		actual, err := store.GetLatestState(ctx, "persistence-1")
		require.NoError(t, err)
		require.NotNil(t, actual)
		assert.True(t, proto.Equal(durableState, actual))
	})
	t.Run("testUpsert", func(t *testing.T) {
		ctx := context.TODO()
		state1, err := anypb.New(&testpb.Account{AccountId: "account-1", AccountBalance: 10.0})
		require.NoError(t, err)
		state2, err := anypb.New(&testpb.Account{AccountId: "account-1", AccountBalance: 20.0})
		require.NoError(t, err)

		store := NewDurableStore(&Config{ObjectStoreURL: "memory:///", DbPath: "test-durable-upsert"})
		require.NoError(t, store.Connect(ctx))

		require.NoError(t, store.WriteState(ctx, &egopb.DurableState{
			PersistenceId: "persistence-1", VersionNumber: 1, ResultingState: state1, Timestamp: 100, Shard: 1,
		}))
		require.NoError(t, store.WriteState(ctx, &egopb.DurableState{
			PersistenceId: "persistence-1", VersionNumber: 2, ResultingState: state2, Timestamp: 200, Shard: 1,
		}))

		actual, err := store.GetLatestState(ctx, "persistence-1")
		require.NoError(t, err)
		require.NotNil(t, actual)
		assert.Equal(t, uint64(2), actual.GetVersionNumber())
	})
	t.Run("testGetLatestStateOnEmpty", func(t *testing.T) {
		ctx := context.TODO()
		store := NewDurableStore(&Config{ObjectStoreURL: "memory:///", DbPath: "test-durable-empty"})
		require.NoError(t, store.Connect(ctx))

		actual, err := store.GetLatestState(ctx, "unknown-pid")
		require.NoError(t, err)
		assert.Nil(t, actual)
	})
	t.Run("testPerActorIsolation", func(t *testing.T) {
		ctx := context.TODO()
		stateA, err := anypb.New(&testpb.Account{AccountId: "account-a"})
		require.NoError(t, err)
		stateB, err := anypb.New(&testpb.Account{AccountId: "account-b"})
		require.NoError(t, err)

		store := NewDurableStore(&Config{ObjectStoreURL: "memory:///", DbPath: "test-durable-isolation"})
		require.NoError(t, store.Connect(ctx))

		require.NoError(t, store.WriteState(ctx, &egopb.DurableState{
			PersistenceId: "pid-a", VersionNumber: 1, ResultingState: stateA, Timestamp: 1, Shard: 1,
		}))
		require.NoError(t, store.WriteState(ctx, &egopb.DurableState{
			PersistenceId: "pid-b", VersionNumber: 1, ResultingState: stateB, Timestamp: 1, Shard: 1,
		}))

		gotA, err := store.GetLatestState(ctx, "pid-a")
		require.NoError(t, err)
		gotB, err := store.GetLatestState(ctx, "pid-b")
		require.NoError(t, err)

		accountA := new(testpb.Account)
		require.NoError(t, gotA.GetResultingState().UnmarshalTo(accountA))
		assert.Equal(t, "account-a", accountA.GetAccountId())

		accountB := new(testpb.Account)
		require.NoError(t, gotB.GetResultingState().UnmarshalTo(accountB))
		assert.Equal(t, "account-b", accountB.GetAccountId())
	})
	t.Run("testPing", func(t *testing.T) {
		ctx := context.TODO()
		store := NewDurableStore(&Config{ObjectStoreURL: "memory:///", DbPath: "test-durable-ping"})
		require.NoError(t, store.Ping(ctx))
		require.NoError(t, store.Disconnect(ctx))
	})
	t.Run("testDurabilityFlushMode", func(t *testing.T) {
		ctx := context.TODO()
		store := NewDurableStore(&Config{
			ObjectStoreURL: "memory:///",
			DbPath:         "test-durable-flush",
			Durability:     DurabilityFlush,
			FlushInterval:  10 * time.Millisecond,
		})
		require.NoError(t, store.Connect(ctx))

		state, err := anypb.New(&testpb.Account{AccountId: "account-42"})
		require.NoError(t, err)
		require.NoError(t, store.WriteState(ctx, &egopb.DurableState{
			PersistenceId: "persistence-1", VersionNumber: 1, ResultingState: state, Timestamp: 1, Shard: 1,
		}))

		actual, err := store.GetLatestState(ctx, "persistence-1")
		require.NoError(t, err)
		require.NotNil(t, actual)

		// Disconnect performs a final synchronous WAL flush and shutdown.
		require.NoError(t, store.Disconnect(ctx))
	})
}
