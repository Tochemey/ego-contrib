# Events Store (Memory Backend)

## Overview
This module implements [eGo](https://github.com/Tochemey/ego)'s event journal on top of HashiCorp's in-memory `memdb`. 
It satisfies the `github.com/tochemey/ego/v3/persistence.EventsStore` interface and is ideal for unit tests, lightweight benchmarks, and prototypes where durability is not required.

## Features
- Full implementation of the EventsStore contract: `WriteEvents`, `PersistenceIDs`, `ReplayEvents`, `GetShardEvents`, `ShardNumbers`, and more
- Uses `hashicorp/go-memdb` for deterministic, thread-safe queries
- Optional `KeepRecordsAfterDisconnect` flag for test scenarios that reuse the store
- Automatic `Connect`/`Disconnect` lifecycle that clears memory unless instructed otherwise

## Installation
```bash
go get github.com/tochemey/ego-contrib/eventstore/memory
```

## Quickstart
```go
package main

import (
	"context"
	"log"
	"time"

	memory "github.com/tochemey/ego-contrib/eventstore/memory"
	"github.com/tochemey/ego/v3/egopb"
	"google.golang.org/protobuf/types/known/anypb"

	accountpb "github.com/acme/billing/proto"
)

func main() {
	ctx := context.Background()

	store := memory.NewEventsStore()
	store.KeepRecordsAfterDisconnect = true // optional: keep data between reconnects during tests

	if err := store.Connect(ctx); err != nil {
		log.Fatalf("connect memory store: %v", err)
	}
	defer store.Disconnect(ctx)

	eventPayload, err := anypb.New(&accountpb.AccountOpened{AccountId: "account-42"})
	if err != nil {
		log.Fatalf("wrap event payload: %v", err)
	}
	statePayload, err := anypb.New(&accountpb.AccountState{AccountId: "account-42", BalanceCents: 1000})
	if err != nil {
		log.Fatalf("wrap state payload: %v", err)
	}

	event := &egopb.Event{
		PersistenceId:  "account-42",
		SequenceNumber: 1,
		Event:          eventPayload,
		ResultingState: statePayload,
		Timestamp:      time.Now().UnixMilli(),
		Shard:          0,
	}

	if err := store.WriteEvents(ctx, []*egopb.Event{event}); err != nil {
		log.Fatalf("append event: %v", err)
	}

	latest, err := store.GetLatestEvent(ctx, "account-42")
	if err != nil {
		log.Fatalf("read last event: %v", err)
	}
	log.Printf("last seq number: %d", latest.GetSequenceNumber())

	events, err := store.ReplayEvents(ctx, "account-42", 1, 10, 100)
	if err != nil {
		log.Fatalf("replay: %v", err)
	}
	log.Printf("replayed %d events", len(events))
}
```

> **Important:** Event and state payloads are stored as protobuf bytes along with their manifests. Import the packages that define those messages so the descriptors are available via `protoregistry.GlobalTypes`.

## Capabilities
- `PersistenceIDs` supports pagination via `pageSize` and `pageToken`
- `GetShardEvents` streams events for a shard after a timestamp offset, helping projection pipelines
- `DeleteEvents` removes all events up to an inclusive sequence number (useful for snapshotting tests)
- `ShardNumbers` exposes which shards currently have events in memory

## Testing
```bash
go test ./...
```

CI pipelines can also run the repository-level `earthly +test` target if Earthly is installed.

## Limitations
- Not suitable for production; data vanishes on process exit (and by default on `Disconnect`)
- Full scans are employed for some operations (e.g., `PersistenceIDs`), so very large datasets will be slower
- No visibility into multi-process coordination; use only within a single test runner
