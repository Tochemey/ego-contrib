# Durable State Store (PostgreSQL)

## Overview

This module persists the durable state of [eGo](https://github.com/Tochemey/ego) entities in PostgreSQL.
It implements `github.com/tochemey/ego/v4/persistence.StateStore` on top of `github.com/jackc/pgx/v5`.

A durable state entity keeps no journal. Only its latest state is stored, as protobuf bytes together with the full name of its message, so it can be unmarshalled back into the right type when the entity restarts.
Each write is an upsert on the persistence id, so the table holds exactly one row per entity.

The store either builds its own connection pool from a `Config` or runs on a `pgxpool.Pool` you already own.

## Schema

Apply the DDL before starting your application:

```bash
psql "postgres://user:pass@localhost:5432/ego?sslmode=disable" -f resources/durablestore_postgres.sql
```

It creates the `states_store` table:

```sql
CREATE TABLE IF NOT EXISTS states_store
(
    persistence_id  VARCHAR(255)          PRIMARY KEY,
    version_number  BIGINT                NOT NULL,
    state_payload   BYTEA                 NOT NULL,
    state_manifest  VARCHAR(255)          NOT NULL,
    timestamp       BIGINT                NOT NULL,
    shard_number    BIGINT                NOT NULL
);
```

The persistence id is the primary key, which is what makes the write an upsert.
`version_number` is incremented by eGo on every state change and `state_manifest` holds the protobuf message name used to rebuild the state.

To keep the table in a schema other than the default one, create it there and set `Config.DBSchema` to that schema name.

## Installation

```bash
go get github.com/tochemey/ego-contrib/durablestore/postgres@vX.Y.Z
```

## HowTo

### Create a store that owns its pool

Give the store a `Config` and it opens a pool on `Connect` and closes it on `Disconnect`:

```go
store := postgres.NewDurableStore(&postgres.Config{
    DBHost:     "127.0.0.1",
    DBPort:     5432,
    DBName:     "ego",
    DBUser:     "ego",
    DBPassword: "secret",
    DBSchema:   "public",
})

if err := store.Connect(ctx); err != nil {
    return err
}
defer store.Disconnect(ctx)
```

`Config` also carries the pool settings. Leave a field empty to take its default:

| Field | Default | Meaning |
|---|---|---|
| `DBSSLMode` | `disable` | SSL mode of the connection |
| `MaxConnections` | 4 | Largest number of connections in the pool |
| `MinConnections` | 0 | Number of connections kept open when idle |
| `MaxConnectionLifetime` | 1 hour | Age at which a connection is closed |
| `MaxConnIdleTime` | 30 minutes | Idle time after which a connection is closed |
| `HealthCheckPeriod` | 1 minute | Interval between health checks of idle connections |

### Run on a pool you own

When your application already has a pool, or when several stores must share one, pass it in.
`Connect` then only pings the pool and `Disconnect` leaves it open:

```go
pool, err := pgxpool.New(ctx, "postgres://ego:secret@127.0.0.1:5432/ego?search_path=public")
if err != nil {
    return err
}
defer pool.Close()

store := postgres.NewDurableStoreWithPool(pool)
if err := store.Connect(ctx); err != nil {
    return err
}
defer store.Disconnect(ctx)
```

The store depends on the `Pool` interface, which declares `Exec`, `Query` and `Ping`.
A `*pgxpool.Pool` satisfies it as is, and so does any wrapper of your own, for instance one that adds tracing.
`Close` is deliberately absent, so a store never closes a pool it did not create.
The same pool can therefore back the event, snapshot, durable state and offset stores of one application.
Those four modules all declare a package named `postgres`, so alias them on import when you use more than one:

```go
import (
    eventstore "github.com/tochemey/ego-contrib/eventstore/postgres"
    snapshotstore "github.com/tochemey/ego-contrib/snapshotstore/postgres"
)
```

### Plug the store into eGo

Pass the store to `ego.WithStateStore`. Durable state deployments that host no event-sourced entity pass `nil` as the events store:

```go
config := ego.NewConfig(nil, ego.WithStateStore(store))

actorSystem, err := goakt.NewActorSystem("accounts", config.GoaktOptions()...)
if err != nil {
    return err
}

engine, err := ego.NewEngine(actorSystem, config)
```

eGo calls `Connect` and `Ping` on the store itself, so connecting by hand is only needed when you use the store outside of an engine.

### Write and read a state directly

`WriteState` upserts the row of the entity. Writing a state whose version is lower than the stored one overwrites it, so let eGo own the version numbering:

```go
payload, err := anypb.New(&accountpb.AccountState{AccountId: "account-42", BalanceCents: 4200})
if err != nil {
    return err
}

err = store.WriteState(ctx, &egopb.DurableState{
    PersistenceId:  "account-42",
    VersionNumber:  2,
    ResultingState: payload,
    Timestamp:      time.Now().UnixMilli(),
    Shard:          3,
})
```

`GetLatestState` returns the stored state, or `nil` when the entity has never been written:

```go
state, err := store.GetLatestState(ctx, "account-42")
if state == nil {
    // no state recorded for this entity
}
```

### Unmarshalling on read

The store resolves the state through `protoregistry.GlobalTypes` using the manifest recorded next to the payload.
Import the generated packages of your state messages in the binary that reads them, otherwise the lookup fails.

## Testing

`go test ./...` runs the suite. Docker must be running, since the integration tests start PostgreSQL with Testcontainers-Go.
The unit tests run the store on a `pgxmock` pool through `NewDurableStoreWithPool`, which needs no database.

From the repository root, `make test/durablestore/postgres` runs the same suite.
