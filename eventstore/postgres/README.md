# Events Store (PostgreSQL)

## Overview
This module persists [eGo](https://github.com/Tochemey/ego) event journals to PostgreSQL. 
It implements `github.com/tochemey/ego/v4/persistence.EventsStore`, batches inserts with `github.com/Masterminds/squirrel`, and relies on `github.com/jackc/pgx/v5` for efficient I/O.

## Features
- Complete EventsStore implementation: `WriteEvents`, `PersistenceIDs`, `ReplayEvents`, `DeleteEvents`, `GetShardEvents`, `ShardOffsets`
- Builds its own `pgxpool.Pool` from `Config`, or runs on a pool you provide through `NewEventsStoreWithPool`
- Batched inserts (default chunk size: 500 events) to avoid PostgreSQL's parameter limit
- Query helpers that return nil slices when no data is found, simplifying upstream logic
- Supports schema-qualified tables through `Config.DBSchema`

## Schema
Apply the bundled DDL before starting your system:
```bash
psql "postgres://user:pass@localhost:5432/ego?sslmode=disable" \
  -f resources/eventstore_postgres.sql
```

The table stores each event as protobuf bytes alongside its manifest so it can be rehydrated accurately:

```sql
CREATE TABLE IF NOT EXISTS events_store(
    persistence_id varchar(255) NOT NULL,
    sequence_number bigint NOT NULL,
    is_deleted boolean DEFAULT FALSE NOT NULL,
    event_payload bytea NOT NULL,
    event_manifest varchar(255) NOT NULL,
    timestamp bigint NOT NULL,
    shard_number bigint NOT NULL,
    encryption_key_id varchar(255) DEFAULT '' NOT NULL,
    is_encrypted boolean DEFAULT FALSE NOT NULL,
    PRIMARY KEY (persistence_id, sequence_number)
);

CREATE INDEX IF NOT EXISTS idx_events_store_timestamp ON events_store(timestamp);
CREATE INDEX IF NOT EXISTS idx_events_store_shard ON events_store(shard_number);
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
	"github.com/tochemey/ego/v4/egopb"
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

	event := &egopb.Event{
		PersistenceId:  "account-42",
		SequenceNumber: 1,
		Event:          eventPayload,
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

> **Reminder:** Event payloads are stored as protobuf bytes plus their manifests. Import the generated protobuf packages for those messages so their descriptors are registered in `protoregistry.GlobalTypes`.

## Bringing your own connection pool
`NewEventsStore` builds and owns a `pgxpool.Pool` from `Config`: `Connect` opens it and `Disconnect` closes it.
`Config` also carries the pool settings (`MaxConnections`, `MinConnections`, `MaxConnectionLifetime`, `MaxConnIdleTime`, `HealthCheckPeriod`) and `DBSSLMode`, with sensible defaults when left empty.

When your application already manages a pool, or when several stores must share one, hand it over with `NewEventsStoreWithPool`:

```go
pool, err := pgxpool.New(ctx, "postgres://ego:secret@127.0.0.1:5432/ego?search_path=public")
if err != nil {
	log.Fatalf("create pool: %v", err)
}
defer pool.Close()

store := pgstore.NewEventsStoreWithPool(pool)
if err := store.Connect(ctx); err != nil { // only pings the pool
	log.Fatalf("connect store: %v", err)
}
defer store.Disconnect(ctx) // never closes a pool it did not create
```

The store only depends on the `Pool` interface (`Exec`, `Query`, `BeginTx`, `Ping`), which `*pgxpool.Pool` satisfies as is.
Any type with those methods works too, for instance a pool wrapped for tracing or `pgxmock.PgxPoolIface` in unit tests.
Ownership stays with the caller: `Disconnect` never closes a pool it did not create, so a single pool can back the event, snapshot, durable state and offset stores at once.

## Testing
- `go test ./...` exercises the full suite
- `eventstore/postgres/helper_test.go` launches PostgreSQL via Testcontainers-Go and offers helpers like `SchemaUtils`
- Repository-wide recipe: run `make test` from the repository root, or `make test/eventstore/postgres` for this module only

## Operational Notes
- `Ping` lazily connects; you can call it from readiness probes
- `WriteEvents` deletes nothing. Use `DeleteEvents` when you roll snapshots forward
- `ShardOffsets` returns every shard mapped to the timestamp of its latest event in a single `GROUP BY` query; eGo uses it to find shards with pending events
- The default batch size is tuned for typical workloads; fork or wrap the store if you need to change it
- Errors from the store include context-rich messages to aid observability (wrap them with your logger before retrying)
