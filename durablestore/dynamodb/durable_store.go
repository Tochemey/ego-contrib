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
func (d DynamoDurableStore) Connect(_ context.Context) error {
	return nil
}

// Disconnect disconnect the journal store
// There is no need to disconnect because the client is stateless
func (DynamoDurableStore) Disconnect(_ context.Context) error {
	return nil
}

// Ping verifies a connection to the database is still alive, establishing a connection if necessary.
// There is no need to ping because the client is stateless
func (d DynamoDurableStore) Ping(_ context.Context) error {
	return nil
}

// WriteState persist durable state for a given persistenceID.
func (d DynamoDurableStore) WriteState(ctx context.Context, state *egopb.DurableState) error {
	bytea, _ := proto.Marshal(state.GetResultingState())
	manifest := string(state.GetResultingState().ProtoReflect().Descriptor().FullName())

	return d.ddb.UpsertItem(ctx, &StateItem{
		PersistenceID: state.GetPersistenceId(),
		VersionNumber: state.GetVersionNumber(),
		StatePayload:  bytea,
		StateManifest: manifest,
		Timestamp:     state.GetTimestamp(),
		ShardNumber:   state.GetShard(),
	})
}

// GetLatestState fetches the latest durable state
func (d DynamoDurableStore) GetLatestState(ctx context.Context, persistenceID string) (*egopb.DurableState, error) {
	result, err := d.ddb.GetItem(ctx, persistenceID)
	if err != nil {
		return nil, err
	}

	return result.ToDurableState()
}
