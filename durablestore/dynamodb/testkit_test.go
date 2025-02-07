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
