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

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/tochemey/ego/v3/egopb"
	"github.com/tochemey/ego/v3/persistence"
	"google.golang.org/protobuf/proto"
)

// DynamoDurableStore implements the DurableStore interface
// and helps persist states in a DynamoDB
type DynamoDurableStore struct {
	ddb database
}

// enforce interface implementation
var _ persistence.StateStore = (*DynamoDurableStore)(nil)

func NewDurableStore(tableName string, client *dynamodb.Client) *DynamoDurableStore {
	return &DynamoDurableStore{
		ddb: newDynamodb(tableName, client),
	}
}

// Connect connects to the journal store
// No connection is needed because the client is stateless
func (DynamoDurableStore) Connect(_ context.Context) error {
	return nil
}

// Disconnect disconnect the journal store
// There is no need to disconnect because the client is stateless
func (DynamoDurableStore) Disconnect(_ context.Context) error {
	return nil
}

// Ping verifies a connection to the database is still alive, establishing a connection if necessary.
// There is no need to ping because the client is stateless
func (DynamoDurableStore) Ping(_ context.Context) error {
	return nil
}

// WriteState persist durable state for a given persistenceID.
func (s DynamoDurableStore) WriteState(ctx context.Context, state *egopb.DurableState) error {
	bytea, _ := proto.Marshal(state.GetResultingState())
	manifest := string(state.GetResultingState().ProtoReflect().Descriptor().FullName())

	return s.ddb.UpsertItem(ctx, &item{
		PersistenceID: state.GetPersistenceId(),
		VersionNumber: state.GetVersionNumber(),
		StatePayload:  bytea,
		StateManifest: manifest,
		Timestamp:     state.GetTimestamp(),
		ShardNumber:   state.GetShard(),
	})
}

// GetLatestState fetches the latest durable state
func (s DynamoDurableStore) GetLatestState(ctx context.Context, persistenceID string) (*egopb.DurableState, error) {
	result, err := s.ddb.GetItem(ctx, persistenceID)
	switch {
	case result == nil && err == nil:
		return nil, nil
	case err != nil:
		return nil, err
	default:
		return result.ToDurableState()
	}
}
