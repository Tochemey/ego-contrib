# Durable State Store (Cassandra)

## Overview
This module persists durable state for [eGo](https://github.com/Tochemey/ego) on Apache Cassandra.
It implements `github.com/tochemey/ego/v3/persistence.StateStore` using `github.com/apache/cassandra-gocql-driver/v2` and stores both the serialized protobuf payload and its manifest so snapshots can be rebuilt later.

## Features
- Implements `github.com/tochemey/ego/v3/persistence.StateStore`
- Cassandra-native upsert semantics via `INSERT`
- Configurable consistency and keyspace
- Simple schema in `resources/states_store.sql`

## Schema
Create the keyspace and table before starting your actor system:
```bash
cqlsh -e "CREATE KEYSPACE IF NOT EXISTS ego WITH replication = {'class': 'SimpleStrategy', 'replication_factor': 1};"
cqlsh -k ego -f resources/states_store.sql
```

The table layout:

| Column           | Type    | Notes                                   |
|------------------|---------|-----------------------------------------|
| `version_number` | `bigint`| Latest durable version                  |
| `persistence_id` | `text`  | Identifier for the persisted entity     |
| `state_payload`  | `blob`  | Serialized protobuf bytes               |
| `state_manifest` | `text`  | Fully qualified protobuf message name   |
| `timestamp`      | `bigint`| Unix epoch milliseconds                 |
| `shard_number`   | `bigint`| Partition key for sharded deployments   |

```sql
CREATE TABLE IF NOT EXISTS states_store (
    version_number  bigint,
    persistence_id  text,
    state_payload   blob,
    state_manifest  text,
    timestamp       bigint,
    shard_number    bigint,
    PRIMARY KEY (shard_number, persistence_id)
);
```

## Installation
```bash
go get github.com/tochemey/ego-contrib/durablestore/cassandra
```

## Quickstart
```go
package main

import (
	"context"
	"log"
	"time"

	cassstore "github.com/tochemey/ego-contrib/durablestore/cassandra"
	"github.com/apache/cassandra-gocql-driver/v2"
	"github.com/tochemey/ego/v3/egopb"
	"google.golang.org/protobuf/types/known/anypb"

	accountpb "github.com/acme/billing/proto" // import your generated protobuf packages
)

func main() {
	ctx := context.Background()

	cfg := &cassstore.Config{
		Cluster:     "127.0.0.1",
		Keyspace:    "ego",
		Consistency: gocql.LocalOne,
	}

	store := cassstore.NewDurableStore(cfg)
	if err := store.Connect(ctx); err != nil {
		log.Fatalf("connect cassandra store: %v", err)
	}
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

> **Reminder:** Ensure your protobuf packages are imported so their descriptors are registered in `protoregistry.GlobalTypes`; otherwise the store cannot rehydrate records.

## Testing
- Local stack: `go test ./...`
- Docker-based harness: `durablestore/cassandra/helper_test.go` spins up Cassandra 5.0.6 using Testcontainers-Go
- CI-friendly recipe: run `earthly +test` from the repository root if you already use Earthly locally

## Operational Notes
- `GetLatestState` returns `(nil, nil)` when no durable state exists
- Cassandra inserts are upserts; each write replaces the latest snapshot for a `PersistenceId`
- Use a stable `Shard` value (for example `0`) if you do not plan to shard durable state
