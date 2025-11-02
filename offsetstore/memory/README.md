# Offset Store (Memory Backend)

## Overview
This module provides an in-memory implementation of eGo's projection offset store. It satisfies `github.com/tochemey/ego/v3/offsetstore.OffsetStore` and is best suited for tests or quick prototypes where resetting progress between runs is acceptable.

## Features
- Implements `WriteOffset`, `GetCurrentOffset`, and `ResetOffset`
- Backed by `hashicorp/go-memdb` for concurrent-safe reads and writes
- Optional `KeepRecordsAfterDisconnect` flag when you want offsets to survive reconnects during the same process
- Uses UUID-backed ordering keys so inserts remain unique without extra coordination

## Installation
```bash
go get github.com/tochemey/ego-contrib/offsetstore/memory
```

## Quickstart
```go
package main

import (
	"context"
	"log"
	"time"

	memory "github.com/tochemey/ego-contrib/offsetstore/memory"
	"github.com/tochemey/ego/v3/egopb"
)

func main() {
	ctx := context.Background()

	store := memory.NewOffsetStore()
	store.KeepRecordsAfterDisconnect = true // keep offsets during reconnects in tests

	if err := store.Connect(ctx); err != nil {
		log.Fatalf("connect offset store: %v", err)
	}
	defer store.Disconnect(ctx)

	offset := &egopb.Offset{
		ShardNumber:    0,
		ProjectionName: "accounts-projection",
		Value:          15,
		Timestamp:      time.Now().UnixMilli(),
	}

	if err := store.WriteOffset(ctx, offset); err != nil {
		log.Fatalf("write offset: %v", err)
	}

	current, err := store.GetCurrentOffset(ctx, &egopb.ProjectionId{
		ShardNumber:    0,
		ProjectionName: "accounts-projection",
	})
	if err != nil {
		log.Fatalf("read offset: %v", err)
	}
	log.Printf("current offset: %d", current.GetValue())

	if err := store.ResetOffset(ctx, "accounts-projection", 0); err != nil {
		log.Fatalf("reset offset: %v", err)
	}
}
```

## Testing
```bash
go test ./...
```

Earthly users can trigger the module tests with `earthly +test` from the repository root.

## Limitations
- Offsets live only in memory; production systems should use a durable backend
- `ResetOffset` scans all rows for a projection—large in-memory datasets may suffer from increased latency
- The store does not coordinate across processes; confine it to single-node integration tests
