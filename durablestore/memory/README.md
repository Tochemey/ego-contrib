# Durable State Store (Memory Backend)

## Overview
This module supplies an in-memory implementation of eGo's `persistence.StateStore` interface. It is perfect for unit tests, demonstrations, and small prototypes where you do not need persistence across process restarts.

## Features
- Fully satisfies `github.com/tochemey/ego/v3/persistence.StateStore`
- Backed by a thread-safe `sync.Map` with atomic connection guards
- Zero external services or schema management
- Automatic cleanup on `Disconnect`; state does not survive process shutdown

## Installation
```bash
go get github.com/tochemey/ego-contrib/durablestore/memory
```

## Quickstart
```go
package main

import (
	"context"
	"log"
	"time"

	memory "github.com/tochemey/ego-contrib/durablestore/memory"
	"github.com/tochemey/ego/v3/egopb"
	"google.golang.org/protobuf/types/known/anypb"

	accountpb "github.com/acme/billing/proto" // import your generated protobuf packages
)

func main() {
	ctx := context.Background()

	store := memory.NewStateStore()
	if err := store.Connect(ctx); err != nil {
		log.Fatalf("connect durable store: %v", err)
	}
	defer store.Disconnect(ctx)

	account := &accountpb.AccountState{AccountId: "account-42", BalanceCents: 4200}
	statePayload, err := anypb.New(account)
	if err != nil {
		log.Fatalf("wrap state payload: %v", err)
	}

	state := &egopb.DurableState{
		PersistenceId:  "account-42",
		VersionNumber:  1,
		ResultingState: statePayload,
		Timestamp:      time.Now().UnixMilli(),
	}

	if err := store.WriteState(ctx, state); err != nil {
		log.Fatalf("write state: %v", err)
	}

	latest, err := store.GetLatestState(ctx, "account-42")
	if err != nil {
		log.Fatalf("read state: %v", err)
	}

	log.Printf("latest version: %d", latest.GetVersionNumber())
}
```

> **Note:** The store uses `protoregistry.GlobalTypes` to hydrate messages. Ensure the protobuf packages that define the messages you persist are imported so their descriptors are registered.

## Testing
```bash
go test ./...
```

## Limitations
- Designed for non-production scenarios; data lives only in memory
- Calling `Disconnect` purges every record (unless the process exits sooner)
- Supports a single logical node; shard coordination must be handled by your tests
