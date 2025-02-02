package dynamodb

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

// DynamodbTestSuite will run the Postgres tests
type DynamodbTestSuite struct {
	suite.Suite
	container *TestContainer
}

// SetupSuite starts the Postgres database engine and set the container
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
