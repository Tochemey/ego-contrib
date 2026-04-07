# Snapshot Store (PostgreSQL)

## Overview
This module wires [eGo](https://github.com/Tochemey/ego)'s snapshot API to PostgreSQL.
It uses `github.com/jackc/pgx/v5` with a connection pool and persists snapshots in the `snapshots_store` table, keeping both the serialized protobuf payload and its manifest so the actor state can be rebuilt when requested.

## Features
- Implements `github.com/tochemey/ego/v4/persistence.SnapshotStore`
- Connection pooling via `pgxpool` with sensible defaults (4 max connections)
- Idempotent `INSERT ... ON CONFLICT` upsert keyed on `(persistence_id, sequence_number)`
- SQL builder based on `github.com/Masterminds/squirrel`

## Schema
Apply the included DDL before starting your actor system:
```bash
psql "postgres://user:pass@localhost:5432/ego?sslmode=disable" \
  -f resources/snapshotstore_postgres.sql
```

The table layout:

| Column              | Type          | Notes                                      |
|---------------------|---------------|--------------------------------------------|
| `persistence_id`    | `VARCHAR(255)`| Composite primary key                      |
| `sequence_number`   | `BIGINT`      | Composite primary key                      |
| `state_payload`     | `BYTEA`       | Serialized protobuf bytes                  |
| `state_manifest`    | `VARCHAR(255)`| Fully qualified protobuf message name      |
| `timestamp`         | `BIGINT`      | Unix epoch milliseconds                    |
| `encryption_key_id` | `VARCHAR(255)`| Encryption key identifier (default empty)  |
| `is_encrypted`      | `BOOLEAN`     | Whether the payload is encrypted           |

```sql
CREATE TABLE IF NOT EXISTS snapshots_store(
    persistence_id varchar(255) NOT NULL,
    sequence_number bigint NOT NULL,
    state_payload bytea NOT NULL,
    state_manifest varchar(255) NOT NULL,
    timestamp bigint NOT NULL,
    encryption_key_id varchar(255) NOT NULL DEFAULT '',
    is_encrypted boolean NOT NULL DEFAULT FALSE,
    PRIMARY KEY (persistence_id, sequence_number)
);
```

> Use the `DBSchema` option to scope the store to a specific schema when required.

## Installation
```bash
go get github.com/tochemey/ego-contrib/snapshotstore/postgres
```

## Quickstart
```go
package main

import (
	"context"
	"log"
	"time"

	snapstore "github.com/tochemey/ego-contrib/snapshotstore/postgres"
	"github.com/tochemey/ego/v4/egopb"
	"google.golang.org/protobuf/types/known/anypb"

	accountpb "github.com/acme/billing/proto" // imaginary protobuf package used for examples
)

func main() {
	ctx := context.Background()

	cfg := &snapstore.Config{
		DBHost:     "127.0.0.1",
		DBPort:     5432,
		DBName:     "ego",
		DBUser:     "ego",
		DBPassword: "secret",
		DBSchema:   "public",
	}

	store := snapstore.NewSnapshotStore(cfg)
	if err := store.Connect(ctx); err != nil {
		log.Fatalf("connect snapshot store: %v", err)
	}
	defer store.Disconnect(ctx)

	payload, err := anypb.New(&accountpb.AccountState{AccountId: "account-42", BalanceCents: 4200})
	if err != nil {
		log.Fatalf("wrap state payload: %v", err)
	}

	snapshot := &egopb.Snapshot{
		PersistenceId:  "account-42",
		SequenceNumber: 1,
		State:          payload,
		Timestamp:      time.Now().UnixMilli(),
	}

	if err := store.WriteSnapshot(ctx, snapshot); err != nil {
		log.Fatalf("persist snapshot: %v", err)
	}

	latest, err := store.GetLatestSnapshot(ctx, "account-42")
	if err != nil {
		log.Fatalf("fetch snapshot: %v", err)
	}
	log.Printf("latest sequence: %d", latest.GetSequenceNumber())
}
```

> **Reminder:** The store relies on `protoregistry.GlobalTypes`. Import the protobuf packages that define your state messages so their descriptors are registered before you read from the database.

## Testing
- Unit and integration suites: `go test ./...`
- Docker-based harness: `snapshotstore/postgres/helper_test.go` spins up PostgreSQL through Testcontainers-Go
- CI-friendly recipe: run `earthly +test` from the repository root if you already use Earthly locally

## Operational Notes
- Connection settings (pool sizes, timeouts) default to conservative values inside `db_config.go`; fork or wrap the store if you need to tune them
- `Ping` automatically opens the connection if it has not been established
- Errors from `WriteSnapshot` include context on failed inserts; bubble them up to your supervisor logic for proper retry handling
