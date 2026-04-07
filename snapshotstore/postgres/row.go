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

package postgres

import (
	"fmt"

	"github.com/tochemey/ego/v4/egopb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/known/anypb"
)

// snapshotRow represents the snapshot store row
type snapshotRow struct {
	PersistenceID   string
	SequenceNumber  uint64
	StatePayload    []byte
	StateManifest   string
	Timestamp       int64
	EncryptionKeyID string `db:"encryption_key_id"`
	IsEncrypted     bool   `db:"is_encrypted"`
}

// ToSnapshot converts row to snapshot
func (x snapshotRow) ToSnapshot() (*egopb.Snapshot, error) {
	// unmarshal the state
	state, err := toProto(x.StateManifest, x.StatePayload)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal the snapshot state: %w", err)
	}

	return &egopb.Snapshot{
		PersistenceId:   x.PersistenceID,
		SequenceNumber:  x.SequenceNumber,
		State:           state,
		Timestamp:       x.Timestamp,
		EncryptionKeyId: x.EncryptionKeyID,
		IsEncrypted:     x.IsEncrypted,
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
