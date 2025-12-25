/*
 * MIT License
 *
 * Copyright (c) 2024-2025 Arsene Tochemey Gandote
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package cassandra

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	"github.com/tochemey/ego/v3/egopb"
	"github.com/tochemey/goakt/test/data/testpb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

// CassandraTestSuite will run the Cassandra tests
type CassandraTestSuite struct {
	suite.Suite
	container *TestContainer
}

// SetupSuite starts the Postgres database engine and set the container
// host and port to use in the tests
func (s *CassandraTestSuite) SetupSuite() {
	s.container = NewTestContainer()
}

func TestCassandraTestSuite(t *testing.T) {
	suite.Run(t, new(CassandraTestSuite))
}

func (s *CassandraTestSuite) TestUpsert() {
	s.Run("Upsert DurableState into Cassandra and read back", func() {
		state, err := anypb.New(&testpb.Account{
			AccountId:      "123",
			AccountBalance: 123,
		})
		s.Assert().NoError(err)

		ctx := context.Background()
		durableState := &egopb.DurableState{
			PersistenceId:  "account_1",
			VersionNumber:  1,
			ResultingState: state,
			Timestamp:      int64(time.Now().UnixNano()),
			Shard:          1,
		}
		store := s.container.GetDurableStore()
		s.Assert().NotNil(store)
		err = store.Connect(ctx)
		s.Assert().NoError(err)
		err = store.WriteState(ctx, durableState)
		s.Assert().NoError(err)

		respItem, err := store.GetLatestState(ctx, durableState.GetPersistenceId())
		proto.Equal(durableState, respItem)
		s.Assert().NoError(err)
	})

	s.Run("Upsert multiple DurableState data into Cassandra and read back", func() {
		state1, err := anypb.New(&testpb.Account{
			AccountId:      "123",
			AccountBalance: 1,
		})
		s.Assert().NoError(err)

		state2, err := anypb.New(&testpb.Account{
			AccountId:      "456",
			AccountBalance: 1,
		})
		s.Assert().NoError(err)

		ctx := context.Background()
		durableState1 := &egopb.DurableState{
			PersistenceId:  "account_1",
			VersionNumber:  1,
			ResultingState: state1,
			Timestamp:      int64(time.Now().UnixNano()),
			Shard:          1,
		}

		durableState2 := &egopb.DurableState{
			PersistenceId:  "account_2",
			VersionNumber:  1,
			ResultingState: state2,
			Timestamp:      int64(time.Now().UnixNano()),
			Shard:          1,
		}
		store := s.container.GetDurableStore()
		s.Assert().NotNil(store)
		err = store.Connect(ctx)
		s.Assert().NoError(err)
		err = store.WriteState(ctx, durableState1)
		s.Assert().NoError(err)
		err = store.WriteState(ctx, durableState2)
		s.Assert().NoError(err)

		respItem, err := store.GetLatestState(ctx, durableState1.GetPersistenceId())
		proto.Equal(durableState1, respItem)
		s.Assert().NoError(err)

		respItem, err = store.GetLatestState(ctx, durableState2.GetPersistenceId())
		proto.Equal(durableState2, respItem)
		s.Assert().NoError(err)
	})

	s.Run("Read non-existent state", func() {
		ctx := context.Background()

		store := s.container.GetDurableStore()
		s.Assert().NotNil(store)
		err := store.Connect(ctx)
		s.Assert().NoError(err)

		respItem, err := store.GetLatestState(ctx, "non-existent-state")
		s.Assert().Nil(respItem)
		s.Assert().NoError(err)
	})
}
