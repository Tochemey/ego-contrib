package dynamodb

import (
	"context"
	"testing"

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

func (s *DynamodbTestSuite) TestPing() {
	s.Run("Ping ddb with valid connection settings", func() {
		client := NewTestContainer().GetDdbClient()
		ds := NewDurableStore(client)
		err := ds.Ping(context.TODO())
		s.Assert().NoError(err)
	})
}
