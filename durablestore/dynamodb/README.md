# Durable State Store (Amazon DynamoDB)

## Overview
This module persists durable state for [eGo](https://github.com/Tochemey/ego) on top of Amazon DynamoDB. 
It fulfils the `github.com/tochemey/ego/v3/persistence.StateStore` contract and stores both the serialized state payload and its protobuf manifest so a snapshot can be reconstructed later.

## Features
- Stateless design: `Connect`, `Disconnect`, and `Ping` are inexpensive no-ops
- `PutItem`- based upsert semantics; the latest write wins per `PersistenceID`
- Stores protobuf payloads alongside the manifest for reliable re-hydration
- Minimal configuration—only provide a table name and a DynamoDB client

## Prerequisites
Create a table that matches the expected schema before you start the actor system:

| Attribute        | Type | Notes                                         |
|------------------|------|-----------------------------------------------|
| `PersistenceID`  | S    | Partition key (hash key)                      |
| `VersionNumber`  | N    | Optional helper for optimistic workflows      |
| `StatePayload`   | B    | Raw protobuf bytes from `proto.Marshal`       |
| `StateManifest`  | S    | Fully qualified protobuf message name         |
| `Timestamp`      | N    | Unix epoch milliseconds                       |
| `ShardNumber`    | N    | Enables sharded durable-state deployments     |

You can provision the table with on-demand billing for local testing. The `testkit.go` helper spins up DynamoDB Local via Docker and creates this schema automatically.

## Installation
```bash
go get github.com/tochemey/ego-contrib/durablestore/dynamodb
```

## Quickstart
```go
package main

import (
	"context"
	"log"
	"time"

	dynamostore "github.com/tochemey/ego-contrib/durablestore/dynamodb"
	"github.com/tochemey/ego/v3/egopb"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"google.golang.org/protobuf/types/known/anypb"

	accountpb "github.com/acme/billing/proto"
)

func main() {
	ctx := context.Background()

	awsCfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("load AWS config: %v", err)
	}

	client := dynamodb.NewFromConfig(awsCfg)
	store := dynamostore.NewDurableStore("states_store", client)
	_ = store.Connect(ctx) // optional; kept for interface symmetry
	defer store.Disconnect(ctx)

	payload, err := anypb.New(&accountpb.AccountState{AccountId: "account-42", BalanceCents: 4200})
	if err != nil {
		log.Fatalf("wrap state payload: %v", err)
	}

	state := &egopb.DurableState{
		PersistenceId:  "account-42",
		VersionNumber:  1,
		ResultingState: payload,
		Timestamp:      time.Now().UnixMilli(),
		Shard:          0,
	}

	if err := store.WriteState(ctx, state); err != nil {
		log.Fatalf("persist state: %v", err)
	}

	snapshot, err := store.GetLatestState(ctx, "account-42")
	if err != nil {
		log.Fatalf("load state: %v", err)
	}
	if snapshot == nil {
		log.Println("no durable state yet")
	}
}
```

> **Tip:** DynamoDB keeps the protobuf manifests as strings. Ensure your protobuf packages are imported so their descriptors are registered in `protoregistry.GlobalTypes`; otherwise the store cannot rehydrate records.

## Testing
- Local stack: `go test ./...` (or use the Earthly target defined in the repository root)
- Integration: see `durablestore/dynamodb/testkit.go` for a Docker-based DynamoDB Local harness you can reuse in your suites

## Operational Notes
- Writes replace the entire item for a `PersistenceID`; add conditional expressions externally if you require optimistic concurrency
- `GetLatestState` returns `(nil, nil)` when no durable state exists
- Handle AWS credentials and retry policies through the standard AWS SDK v2 configuration chain
