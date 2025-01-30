package dynamodb

import (
	"context"
	"fmt"
	"testing"
	// "time"

	// "github.com/google/uuid"
	"github.com/stretchr/testify/suite"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
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
	s.Run("with valid connection settings", func() {
		cfg, err := config.LoadDefaultConfig(context.TODO())
		client := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
			o.BaseEndpoint = aws.String(s.container.address+"assd")
		})

		a, _ := client.ListTables(context.TODO(), &dynamodb.ListTablesInput{})
		fmt.Println(a)
		s.Assert().NoError(err)
	})
}
