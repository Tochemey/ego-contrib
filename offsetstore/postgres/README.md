# Offset Store (PostgreSQL)

## Overview
This package backs [eGo](https://github.com/Tochemey/ego) projection offsets with PostgreSQL. 
It fulfils `github.com/tochemey/ego/v4/offsetstore.OffsetStore`, managing per-projection, per-shard offsets in a single table while handling the protobuf plumbing for you.

## Features
- Implements the complete OffsetStore contract (`WriteOffset`, `GetCurrentOffset`, `ResetOffset`, `Ping`, lifecycle methods)
- `INSERT ... ON CONFLICT` upsert keyed on `(projection_name, shard_number)` guarantees a single row per projection/shard pair
- Uses `pgxpool` under the hood with safe default connection settings, or bring your own pool through `NewOffsetStoreWithPool`
- Schema-qualified deployments via `Config.DBSchema`

## Schema
Run the included DDL before your application starts:
```bash
psql "postgres://user:pass@localhost:5432/ego?sslmode=disable" \
  -f resources/offsetstore_postgres.sql
```

Table columns:

| Column           | Type          | Notes                                         |
|------------------|---------------|-----------------------------------------------|
| `projection_name`| `VARCHAR(255)`| Part of the composite primary key             |
| `shard_number`   | `BIGINT`      | Part of the composite primary key             |
| `current_offset` | `BIGINT`      | Latest processed offset for that shard        |
| `timestamp`      | `BIGINT`      | Unix epoch milliseconds of the latest update  |

## Installation
```bash
go get github.com/tochemey/ego-contrib/offsetstore/postgres
```

## Quickstart
```go
package main

import (
	"context"
	"log"
	"time"

	pgstore "github.com/tochemey/ego-contrib/offsetstore/postgres"
	"github.com/tochemey/ego/v4/egopb"
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

	store := pgstore.NewOffsetStore(cfg)
	if err := store.Connect(ctx); err != nil {
		log.Fatalf("connect offset store: %v", err)
	}
	defer store.Disconnect(ctx)

	offset := &egopb.Offset{
		ShardNumber:    0,
		ProjectionName: "accounts-projection",
		Value:          42,
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

## Bringing your own connection pool
`NewOffsetStore` builds and owns a `pgxpool.Pool` from `Config`: `Connect` opens it and `Disconnect` closes it.
`Config` also carries the pool settings (`MaxConnections`, `MinConnections`, `MaxConnectionLifetime`, `MaxConnIdleTime`, `HealthCheckPeriod`) and `DBSSLMode`, with sensible defaults when left empty.

When your application already manages a pool, or when several stores must share one, hand it over with `NewOffsetStoreWithPool`:

```go
pool, err := pgxpool.New(ctx, "postgres://ego:secret@127.0.0.1:5432/ego?search_path=public")
if err != nil {
	log.Fatalf("create pool: %v", err)
}
defer pool.Close()

store := pgstore.NewOffsetStoreWithPool(pool)
if err := store.Connect(ctx); err != nil { // only pings the pool
	log.Fatalf("connect store: %v", err)
}
defer store.Disconnect(ctx) // never closes a pool it did not create
```

The store only depends on the `Pool` interface (`Exec`, `Query`, `Ping`), which `*pgxpool.Pool` satisfies as is.
Any type with those methods works too, for instance a pool wrapped for tracing or `pgxmock.PgxPoolIface` in unit tests.
Ownership stays with the caller: `Disconnect` never closes a pool it did not create, so a single pool can back the event, snapshot, durable state and offset stores at once.

## Testing
- Run all module tests: `go test ./...`
- Docker-based harness: `offsetstore/postgres/helper_test.go` spins up PostgreSQL through Testcontainers-Go
- Repository-wide recipe: run `make test` from the repository root, or `make test/offsetstore/postgres` for this module only

## Operational Notes
- `WriteOffset` upserts the row of the projection/shard pair, so the table never holds more than one row per pair
- `ResetOffset` updates every shard of the provided projection in a single statement
- Pool settings (sizes, lifetimes, health checks) and the SSL mode are fields of `Config`; leave them empty for the defaults, or bring your own pool
- Remember to import the protobuf packages that describe your offsets (eGo registers them automatically, but custom messages must also be in scope)
