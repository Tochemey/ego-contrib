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

package sqlite

import (
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/tochemey/ego/v4/egopb"
)

// row represents the events store row
type row struct {
	PersistenceID   string
	SequenceNumber  uint64
	IsDeleted       bool
	EventPayload    []byte
	EventManifest   string
	Timestamp       int64
	ShardNumber     uint64
	EncryptionKeyID string `db:"encryption_key_id"`
	IsEncrypted     bool   `db:"is_encrypted"`
}

// ToEvent converts a row to an event
func (x row) ToEvent() (*egopb.Event, error) {
	event, err := toProto(x.EventManifest, x.EventPayload)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal the journal event: %w", err)
	}

	return &egopb.Event{
		PersistenceId:   x.PersistenceID,
		SequenceNumber:  x.SequenceNumber,
		IsDeleted:       x.IsDeleted,
		Event:           event,
		Timestamp:       x.Timestamp,
		Shard:           x.ShardNumber,
		EncryptionKeyId: x.EncryptionKeyID,
		IsEncrypted:     x.IsEncrypted,
	}, nil
}

// rows defines a list of row
type rows []*row

// ToEvents converts rows to events
func (x rows) ToEvents() ([]*egopb.Event, error) {
	events := make([]*egopb.Event, 0, len(x))
	for _, row := range x {
		event, err := row.ToEvent()
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}

	return events, nil
}

// toProto converts a byte array given its manifest into a valid proto message
func toProto(manifest string, bytea []byte) (*anypb.Any, error) {
	mt, err := protoregistry.GlobalTypes.FindMessageByName(protoreflect.FullName(manifest))
	if err != nil {
		return nil, err
	}

	pm := mt.New().Interface()
	if err := proto.Unmarshal(bytea, pm); err != nil {
		return nil, err
	}

	if cast, ok := pm.(*anypb.Any); ok {
		return cast, nil
	}
	return nil, fmt.Errorf("failed to unpack message=%s", manifest)
}
