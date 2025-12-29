# Offset Store (PostgreSQL)

## Overview
This package backs [eGo](https://github.com/Tochemey/ego) projection offsets with PostgreSQL. 
It fulfils `github.com/tochemey/ego/v3/offsetstore.OffsetStore`, managing per-projection, per-shard offsets in a single table while handling the protobuf plumbing for you.

## Features
- Implements the complete OffsetStore contract (`WriteOffset`, `GetCurrentOffset`, `ResetOffset`, `Ping`, lifecycle methods)
- Transactional delete-and-insert semantics guarantee a single row per projection/shard pair
- Uses `pgxpool` under the hood with safe default connection settings
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
	"github.com/tochemey/ego/v3/egopb"
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

## Testing
- Run all module tests: `go test ./...`
- Use `offsetstore/postgres/internal/testkit.go` to spin up PostgreSQL via Testcontainers-Go when writing integration suites
- Earthly users can execute the repository target: `earthly +test`

## Operational Notes
- `WriteOffset` removes any existing row for the projection/shard before inserting the new offset to keep the table tidy
- `ResetOffset` updates every shard for the provided projection in a single transaction
- Connection pool defaults live in `internal/config.go`; fork or wrap the store if you need to tune them
- Remember to import the protobuf packages that describe your offsets (eGo registers them automatically, but custom messages must also be in scope)
