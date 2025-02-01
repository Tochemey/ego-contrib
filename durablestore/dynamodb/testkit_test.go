package dynamodb

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/stretchr/testify/suite"
)

type testkitSuite struct {
	suite.Suite
	container *TestContainer
}

// SetupSuite starts the database database engine and set the container
// host and port to use in the tests
func (s *testkitSuite) SetupSuite() {
	s.container = NewTestContainer()
}

func (s *testkitSuite) TearDownSuite() {
	s.container.Cleanup()
}

// In order for 'go test' to run this suite, we need to create
// a normal test function and pass our suite to suite.Run
func TestTestKitSuite(t *testing.T) {
	suite.Run(t, new(testkitSuite))
}

func (s *testkitSuite) TestCreateTable() {
	s.Run("happy path", func() {
		ctx := context.TODO()

		client := s.container.GetDdbClient(ctx)
		err := s.container.CreateTable(ctx, "test-table", client)
		s.Assert().NoError(err)

		result, err := client.ListTables(ctx, &dynamodb.ListTablesInput{})
		s.Assert().NoError(err)
		s.Assert().Equal([]string{"test-table"}, result.TableNames)
	})
}
