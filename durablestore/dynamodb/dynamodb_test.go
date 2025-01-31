package dynamodb

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

// account is a test struct
type account struct {
	AccountID   string
	AccountName string
}

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

func (s *DynamodbTestSuite) TestConnect() {
	s.Run("Ping ddb with valid connection settings", func() {
		client := NewTestContainer().GetDdbClient()
		ds := NewDurableStore(client)
		err := ds.Ping(context.TODO())
		s.Assert().NoError(err)
	})
}
