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

package dynamodb

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

// DynamodbTestSuite will run the DynamoDB tests
type DynamodbTestSuite struct {
	suite.Suite
	container *TestContainer
}

// SetupSuite starts the DynamoDB local container and set the container
// host and port to use in the tests
func (s *DynamodbTestSuite) SetupSuite() {
	s.container = NewTestContainer()
}

func TestDynamodbTestSuite(t *testing.T) {
	suite.Run(t, new(DynamodbTestSuite))
}

func (s *DynamodbTestSuite) TestUpsert() {
	s.Run("Upsert StateItem into DynamoDB and read back", func() {
		store := s.container.GetDurableStore()
		persistenceID := "account_1"
		stateItem := &item{
			PersistenceID: persistenceID,
			VersionNumber: 1,
			StatePayload:  []byte{},
			StateManifest: "manifest",
			Timestamp:     int64(time.Now().UnixNano()),
			ShardNumber:   1,
		}
		err := store.ddb.UpsertItem(context.Background(), stateItem)
		s.Assert().NoError(err)

		respItem, err := store.ddb.GetItem(context.Background(), persistenceID)
		s.Assert().Equal(stateItem, respItem)
		s.Assert().NoError(err)
	})
}
