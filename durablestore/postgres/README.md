# Durable State Store (PostgreSQL)

## Overview
This module wires [eGo](https://github.com/Tochemey/ego)'s durable state API to PostgreSQL. 
It uses `github.com/jackc/pgx/v5` with a small connection pool and persists snapshots in the `states_store` table, keeping both the serialized protobuf payload and its manifest so the actor state can be rebuilt when requested.

## Features
- Implements `github.com/tochemey/ego/v3/persistence.StateStore`
- Connection pooling via `pgxpool` with sensible defaults (4 max connections)
- Idempotent `INSERT ... ON CONFLICT` upsert for each `PersistenceID`
- SQL builder based on `github.com/Masterminds/squirrel`

## Schema
Apply the included DDL before starting your actor system:
```bash
psql "postgres://user:pass@localhost:5432/ego?sslmode=disable" \
  -f resources/durablestore_postgres.sql
```

The table layout:

| Column           | Type          | Notes                                                      |
|------------------|---------------|------------------------------------------------------------|
| `persistence_id` | `VARCHAR(255)`| Primary key                                                |
| `version_number` | `BIGINT`      | Latest durable version for that persistence ID             |
| `state_payload`  | `BYTEA`       | Serialized protobuf bytes                                  |
| `state_manifest` | `VARCHAR(255)`| Fully qualified protobuf message name                      |
| `timestamp`      | `BIGINT`      | Unix epoch milliseconds                                    |
| `shard_number`   | `BIGINT`      | Optional helper for sharded deployments                    |

```sql
CREATE TABLE IF NOT EXISTS states_store
(
    persistence_id  VARCHAR(255) PRIMARY KEY,
    version_number  BIGINT       NOT NULL,
    state_payload   BYTEA        NOT NULL,
    state_manifest  VARCHAR(255) NOT NULL,
    timestamp       BIGINT       NOT NULL,
    shard_number    BIGINT       NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_states_store_version_number
    ON states_store (version_number);
```

> Use the `DBSchema` option to scope the store to a specific schema when required.

## Installation
```bash
go get github.com/tochemey/ego-contrib/durablestore/postgres
```

## Quickstart
```go
package main

import (
	"context"
	"log"
	"time"

	pgstore "github.com/tochemey/ego-contrib/durablestore/postgres"
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

	store := pgstore.NewDurableStore(cfg)
	if err := store.Connect(ctx); err != nil {
		log.Fatalf("connect postgres store: %v", err)
	}
	defer store.Disconnect(ctx)

	payload, err := anypb.New(&accountpb.AccountState{AccountId: "account-42", BalanceCents: 4200})
	if err != nil {
		log.Fatalf("wrap state payload: %v", err)
	}

	state := &egopb.DurableState{
		PersistenceId:  "account-42",
		VersionNumber:  2,
		ResultingState: payload,
		Timestamp:      time.Now().UnixMilli(),
		Shard:          1,
	}

	if err := store.WriteState(ctx, state); err != nil {
		log.Fatalf("persist state: %v", err)
	}

	snapshot, err := store.GetLatestState(ctx, "account-42")
	if err != nil {
		log.Fatalf("fetch state: %v", err)
	}
	log.Printf("latest version: %d", snapshot.GetVersionNumber())
}
```

> **Reminder:** The store relies on `protoregistry.GlobalTypes`. Import the protobuf packages that define your state messages so their descriptors are registered before you read from the database.

## Testing
- Unit and integration suites: `go test ./...`
- Docker-based harness: `durablestore/postgres/helper_test.go` spins up PostgreSQL 11 through Testcontainers-Go and exposes helpers such as `SchemaUtils`
- CI-friendly recipe: run `earthly +test` from the repository root if you already use Earthly locally

## Operational Notes
- Connection settings (pool sizes, timeouts) default to conservative values inside `db_config.go`; fork or wrap the store if you need to tune them
- `Ping` automatically opens the connection if it has not been established
- Errors from `WriteState` include context on failed inserts or rollbacks; bubble them up to your supervisor logic for proper retry handling
