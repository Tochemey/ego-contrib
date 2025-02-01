package dynamodb

import (
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/tochemey/ego/v3/egopb"
)

type item struct {
	PersistenceID string // Partition key
	VersionNumber uint64
	StatePayload  []byte
	StateManifest string
	Timestamp     int64
	ShardNumber   uint64
}

// ToDurableState convert row to durable state
func (x item) ToDurableState() (*egopb.DurableState, error) {
	// unmarshal the event and the state
	state, err := toProto(x.StateManifest, x.StatePayload)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal the durable state: %w", err)
	}

	return &egopb.DurableState{
		PersistenceId:  x.PersistenceID,
		ResultingState: state,
		Timestamp:      x.Timestamp,
	}, nil
}

// toProto converts a byte array given its manifest into a valid proto message
func toProto(manifest string, bytea []byte) (*anypb.Any, error) {
	mt, err := protoregistry.GlobalTypes.FindMessageByName(protoreflect.FullName(manifest))
	if err != nil {
		return nil, err
	}

	pm := mt.New().Interface()
	err = proto.Unmarshal(bytea, pm)
	if err != nil {
		return nil, err
	}

	if cast, ok := pm.(*anypb.Any); ok {
		return cast, nil
	}
	return nil, fmt.Errorf("failed to unpack message=%s", manifest)
}
