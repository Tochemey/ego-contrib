# Snapshot Store (PostgreSQL)

## Overview

This module persists the snapshots of [eGo](https://github.com/Tochemey/ego) event-sourced entities in PostgreSQL.
It implements `github.com/tochemey/ego/v4/persistence.SnapshotStore` on top of `github.com/jackc/pgx/v5`.

A snapshot is the state of an entity at a given sequence number.
Recovering an entity from its latest snapshot and replaying only the events written after it is far cheaper than replaying a whole journal.
The state is stored as protobuf bytes together with the full name of its message, so it can be unmarshalled back into the right type.

The store either builds its own connection pool from a `Config` or runs on a `pgxpool.Pool` you already own.

## Schema

Apply the DDL before starting your application:

```bash
psql "postgres://user:pass@localhost:5432/ego?sslmode=disable" -f resources/snapshotstore_postgres.sql
```

It creates the `snapshots_store` table:

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

The primary key makes a persistence id and a sequence number unique together, so an entity keeps several snapshots and writing the same sequence number twice replaces the row instead of failing.
The encryption columns record whether the payload was encrypted and under which key, when eGo is configured with an encryptor.

To keep the table in a schema other than the default one, create it there and set `Config.DBSchema` to that schema name.

## Installation

```bash
go get github.com/tochemey/ego-contrib/snapshotstore/postgres@vX.Y.Z
```

## HowTo

### Create a store that owns its pool

Give the store a `Config` and it opens a pool on `Connect` and closes it on `Disconnect`:

```go
store := postgres.NewSnapshotStore(&postgres.Config{
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

store := postgres.NewSnapshotStoreWithPool(pool)
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

Pass the store to `ego.WithSnapshotStore`, alongside the events store whose entities are being snapshotted:

```go
config := ego.NewConfig(eventsStore, ego.WithSnapshotStore(store))

actorSystem, err := goakt.NewActorSystem("accounts", config.GoaktOptions()...)
if err != nil {
    return err
}

engine, err := ego.NewEngine(actorSystem, config)
```

eGo decides when to take a snapshot and calls the store itself, so writing snapshots by hand is only needed when you use the store outside of an engine.

### Write and read a snapshot directly

`WriteSnapshot` inserts the snapshot, or replaces it when one already exists at that sequence number:

```go
payload, err := anypb.New(&accountpb.AccountState{AccountId: "account-42", BalanceCents: 4200})
if err != nil {
    return err
}

err = store.WriteSnapshot(ctx, &egopb.Snapshot{
    PersistenceId:  "account-42",
    SequenceNumber: 100,
    State:          payload,
    Timestamp:      time.Now().UnixMilli(),
})
```

`GetLatestSnapshot` returns the snapshot with the highest sequence number, or `nil` when the entity has none:

```go
snapshot, err := store.GetLatestSnapshot(ctx, "account-42")
if snapshot == nil {
    // recover from the journal alone
}
```

`DeleteSnapshots` prunes the older snapshots of an entity up to a sequence number, included.
Keep the latest one, otherwise recovery falls back to a full replay:

```go
err := store.DeleteSnapshots(ctx, "account-42", snapshot.GetSequenceNumber()-1)
```

### Unmarshalling on read

The store resolves the state through `protoregistry.GlobalTypes` using the manifest recorded next to the payload.
Import the generated packages of your state messages in the binary that reads them, otherwise the lookup fails.

## Testing

`go test ./...` runs the suite. Docker must be running, since the integration tests start PostgreSQL with Testcontainers-Go.
The unit tests run the store on a `pgxmock` pool through `NewSnapshotStoreWithPool`, which needs no database.

From the repository root, `make test/snapshotstore/postgres` runs the same suite.
