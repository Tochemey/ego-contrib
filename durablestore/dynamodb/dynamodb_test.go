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
		ddb := NewTestContainer().GetDdbClient()
		persistenceId := "account_1"
		stateItem := &StateItem{
			PersistenceID: persistenceId,
			StatePayload:  []byte{},
			StateManifest: "manifest",
			Timestamp:     int64(time.Now().UnixNano()),
		}
		err := ddb.ddb.UpsertItem(context.Background(), stateItem)
		s.Assert().NoError(err)

		respItem, err := ddb.ddb.GetItem(context.Background(), persistenceId)
		s.Assert().Equal(stateItem, respItem)
		s.Assert().NoError(err)
	})
}
