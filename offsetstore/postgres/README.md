# Offset Store (PostgreSQL)

## Overview

This module persists the offsets of [eGo](https://github.com/Tochemey/ego) projections in PostgreSQL.
It implements `github.com/tochemey/ego/v4/offsetstore.OffsetStore` on top of `github.com/jackc/pgx/v5`.

A projection reads the events of each shard in order and records how far it got.
That mark is the offset, and it is the timestamp of the last event the projection handled.
Storing it is what lets a projection resume where it stopped instead of reprocessing a whole journal after a restart.

Offsets are kept per projection and per shard, so the projections of one application advance independently and a projection can progress at a different pace on each shard.

The store either builds its own connection pool from a `Config` or runs on a `pgxpool.Pool` you already own.

## Schema

Apply the DDL before starting your application:

```bash
psql "postgres://user:pass@localhost:5432/ego?sslmode=disable" -f resources/offsetstore_postgres.sql
```

It creates the `offsets_store` table:

```sql
CREATE TABLE IF NOT EXISTS offsets_store
(
    projection_name VARCHAR(255) NOT NULL,
    shard_number    BIGINT       NOT NULL,
    current_offset  BIGINT       NOT NULL,
    timestamp       BIGINT       NOT NULL,
    PRIMARY KEY (projection_name, shard_number)
);
```

The primary key makes a projection name and a shard number unique together, so each write is an upsert and the table never holds more than one row per pair.
`current_offset` is the position reached and `timestamp` records when it was last written.

To keep the table in a schema other than the default one, create it there and set `Config.DBSchema` to that schema name.

## Installation

```bash
go get github.com/tochemey/ego-contrib/offsetstore/postgres@vX.Y.Z
```

## HowTo

### Create a store that owns its pool

Give the store a `Config` and it opens a pool on `Connect` and closes it on `Disconnect`:

```go
store := postgres.NewOffsetStore(&postgres.Config{
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

store := postgres.NewOffsetStoreWithPool(pool)
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

Pass the store to `ego.WithOffsetStore`, alongside the events store the projections read from:

```go
config := ego.NewConfig(eventsStore, ego.WithOffsetStore(store))

actorSystem, err := goakt.NewActorSystem("accounts", config.GoaktOptions()...)
if err != nil {
    return err
}

engine, err := ego.NewEngine(actorSystem, config)
```

eGo commits offsets as its projections advance, so writing them by hand is only needed when you use the store outside of an engine.

### Write and read an offset directly

`WriteOffset` upserts the row of a projection and shard. An offset that is missing or empty is rejected:

```go
err := store.WriteOffset(ctx, &egopb.Offset{
    ProjectionName: "accounts-projection",
    ShardNumber:    3,
    Value:          1712345678901,
    Timestamp:      time.Now().UnixMilli(),
})
```

`GetCurrentOffset` returns the offset reached on one shard, or `nil` when the projection never wrote it:

```go
offset, err := store.GetCurrentOffset(ctx, &egopb.ProjectionId{
    ProjectionName: "accounts-projection",
    ShardNumber:    3,
})
if offset == nil {
    // the projection has not started on this shard
}
```

### Replay a projection from the start

`ResetOffset` sets one value on every shard of a projection in a single statement, leaving other projections untouched.
Passing `0` makes the projection reprocess its whole journal on the next run:

```go
err := store.ResetOffset(ctx, "accounts-projection", 0)
```

Stop the projection before resetting it, otherwise it keeps committing offsets while you rewind them.
Reprocessing replays events the projection already handled, so its handler has to tolerate that.

## Testing

`go test ./...` runs the suite. Docker must be running, since the integration tests start PostgreSQL with Testcontainers-Go.
The unit tests run the store on a `pgxmock` pool through `NewOffsetStoreWithPool`, which needs no database.

From the repository root, `make test/offsetstore/postgres` runs the same suite.
