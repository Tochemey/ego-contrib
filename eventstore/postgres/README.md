# Events Store (PostgreSQL)

## Overview

This module persists [eGo](https://github.com/Tochemey/ego) event journals in PostgreSQL.
It implements `github.com/tochemey/ego/v4/persistence.EventsStore` on top of `github.com/jackc/pgx/v5`.

Each event is stored as protobuf bytes together with the full name of its message, so it can be unmarshalled back into the right type on replay.
Writes go through a transaction and are batched, 500 events per statement by default, which keeps every statement below PostgreSQL's limit of 65535 bind parameters.

The store either builds its own connection pool from a `Config` or runs on a `pgxpool.Pool` you already own.

## Schema

Apply the DDL before starting your application:

```bash
psql "postgres://user:pass@localhost:5432/ego?sslmode=disable" -f resources/eventstore_postgres.sql
```

It creates the `events_store` table:

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

The primary key makes a persistence id and a sequence number unique together.
The two indexes serve the shard queries eGo projections run.

To keep the table in a schema other than the default one, create it there and set `Config.DBSchema` to that schema name.

## Installation

```bash
go get github.com/tochemey/ego-contrib/eventstore/postgres@vX.Y.Z
```

## HowTo

### Create a store that owns its pool

Give the store a `Config` and it opens a pool on `Connect` and closes it on `Disconnect`:

```go
store := postgres.NewEventsStore(&postgres.Config{
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

store := postgres.NewEventsStoreWithPool(pool)
if err := store.Connect(ctx); err != nil {
    return err
}
defer store.Disconnect(ctx)
```

The store depends on the `Pool` interface, which declares `Exec`, `Query`, `BeginTx` and `Ping`.
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

The events store is the first argument of `ego.NewConfig`:

```go
config := ego.NewConfig(store)

actorSystem, err := goakt.NewActorSystem("accounts", config.GoaktOptions()...)
if err != nil {
    return err
}

engine, err := ego.NewEngine(actorSystem, config)
```

eGo calls `Connect` and `Ping` on the store itself, so connecting by hand is only needed when you use the store outside of an engine.

### Write and read events directly

`WriteEvents` persists a batch in one transaction. Either all events land or none do:

```go
payload, err := anypb.New(&accountpb.AccountOpened{AccountId: "account-42"})
if err != nil {
    return err
}

err = store.WriteEvents(ctx, []*egopb.Event{
    {
        PersistenceId:  "account-42",
        SequenceNumber: 1,
        Event:          payload,
        Timestamp:      time.Now().UnixMilli(),
        Shard:          3,
    },
})
```

`ReplayEvents` returns the events of one entity between two sequence numbers, both included, ordered by sequence number:

```go
events, err := store.ReplayEvents(ctx, "account-42", 1, 100, 500)
```

`GetLatestEvent` returns the event with the highest sequence number, or `nil` when the entity has no event yet:

```go
latest, err := store.GetLatestEvent(ctx, "account-42")
if latest == nil {
    // nothing recorded for this entity
}
```

`DeleteEvents` removes the events of an entity up to a sequence number, included. Use it after a snapshot has been taken:

```go
err := store.DeleteEvents(ctx, "account-42", latest.GetSequenceNumber())
```

### Read a shard for projections

`ShardOffsets` maps every shard that holds events to the timestamp of its most recent event, in a single grouped query.
eGo compares that map against the offsets a projection has committed to know which shards have work pending:

```go
offsets, err := store.ShardOffsets(ctx) // for instance map[uint64]int64{3: 1712345678901}
```

`GetShardEvents` then reads the next events of a shard after an offset, and returns the offset to pass on the next call:

```go
events, nextOffset, err := store.GetShardEvents(ctx, 3, offset, 100)
```

### Unmarshalling on replay

The store resolves each event through `protoregistry.GlobalTypes` using the manifest recorded next to the payload.
Import the generated packages of your event messages in the binary that reads them, otherwise the lookup fails.

## Testing

`go test ./...` runs the suite. Docker must be running, since the integration tests start PostgreSQL with Testcontainers-Go.
The unit tests run the store on a `pgxmock` pool through `NewEventsStoreWithPool`, which needs no database.

From the repository root, `make test/eventstore/postgres` runs the same suite.
