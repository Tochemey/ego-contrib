# Events Store (PostgreSQL)

## Overview
This module persists [eGo](https://github.com/Tochemey/ego) event journals to PostgreSQL. 
It implements `github.com/tochemey/ego/v3/persistence.EventsStore`, batches inserts with `github.com/Masterminds/squirrel`, and relies on `github.com/jackc/pgx/v5` for efficient I/O.

## Features
- Complete EventsStore implementation: `WriteEvents`, `PersistenceIDs`, `ReplayEvents`, `DeleteEvents`, `GetShardEvents`, `ShardNumbers`
- Batched inserts (default chunk size: 500 events) to avoid PostgreSQL's parameter limit
- Query helpers that return nil slices when no data is found, simplifying upstream logic
- Utility packages (`schema_utils.go`, `testkit.go`) for integration testing with Dockertest
- Supports schema-qualified tables through `Config.DBSchema`

## Schema
Apply the bundled DDL before starting your system:
```bash
psql "postgres://user:pass@localhost:5432/ego?sslmode=disable" \
  -f resources/eventstore_postgres.sql
```

The table layout stores both event and state payloads alongside their protobuf manifests so they can be rehydrated accurately:

```sql
CREATE TABLE IF NOT EXISTS events_store
(
    persistence_id  VARCHAR(255) NOT NULL,
    sequence_number BIGINT       NOT NULL,
    is_deleted      BOOLEAN      NOT NULL DEFAULT FALSE,
    event_payload   BYTEA        NOT NULL,
    event_manifest  VARCHAR(255) NOT NULL,
    state_payload   BYTEA        NOT NULL,
    state_manifest  VARCHAR(255) NOT NULL,
    timestamp       BIGINT       NOT NULL,
    shard_number    BIGINT       NOT NULL,
    PRIMARY KEY (persistence_id, sequence_number)
);

CREATE INDEX IF NOT EXISTS idx_events_store_persistence_id
    ON events_store (persistence_id);
CREATE INDEX IF NOT EXISTS idx_events_store_seqnumber
    ON events_store (sequence_number);
CREATE INDEX IF NOT EXISTS idx_events_store_timestamp
    ON events_store (timestamp);
CREATE INDEX IF NOT EXISTS idx_events_store_shard
    ON events_store (shard_number);
```

## Installation
```bash
go get github.com/tochemey/ego-contrib/eventstore/postgres
```

## Quickstart
```go
package main

import (
	"context"
	"log"
	"time"

	pgstore "github.com/tochemey/ego-contrib/eventstore/postgres"
	"github.com/tochemey/ego/v3/egopb"
	"google.golang.org/protobuf/types/known/anypb"

	accountpb "github.com/acme/billing/proto" // imaginary protobuf package used for examples
)

func main() {
	ctx := context.Background()

	cfg := &pgstore.Config{
		DBHost:     "127.0.0.1",
		DBPort:     5432,
		DBName:     "ego",
		DBUser:     "ego",
		DBPassword: "secret",
		DBSchema:   "public",
	}

	store := pgstore.NewEventsStore(cfg)
	if err := store.Connect(ctx); err != nil {
		log.Fatalf("connect postgres store: %v", err)
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

	replayed, err := store.ReplayEvents(ctx, "account-42", 1, 100, 100)
	if err != nil {
		log.Fatalf("replay: %v", err)
	}
	log.Printf("replayed %d event(s)", len(replayed))

	next, offset, err := store.GetShardEvents(ctx, 0, event.GetTimestamp()-1, 100)
	if err != nil {
		log.Fatalf("poll shard: %v", err)
	}
	log.Printf("next offset %d, shard events %d", offset, len(next))
}
```

> **Reminder:** Event and state payloads are stored as protobuf bytes plus their manifests. Import the generated protobuf packages for those messages so their descriptors are registered in `protoregistry.GlobalTypes`.

## Testing
- `go test ./...` exercises the full suite
- `eventstore/postgres/testkit.go` launches PostgreSQL via Dockertest and offers helpers like `SchemaUtils`
- The repository-level `earthly +test` target runs all module tests if you already rely on Earthly

## Operational Notes
- `Ping` lazily connects; you can call it from readiness probes
- `WriteEvents` deletes nothing—use `DeleteEvents` when you roll snapshots forward
- The default batch size is tuned for typical workloads; fork or wrap the store if you need to change it
- Errors from the store include context-rich messages to aid observability (wrap them with your logger before retrying)
