# Durable State Store (SQLite)

## Overview

This module persists the durable state of [eGo](https://github.com/Tochemey/ego) entities in SQLite.
It implements `github.com/tochemey/ego/v4/persistence.StateStore` on top of `modernc.org/sqlite`, a pure Go driver, so the module builds without cgo and cross-compiles like any other Go package.

SQLite keeps everything in a single file and needs no server, which suits embedded deployments, edge nodes, command line tools and tests.
It allows one writer at a time, so it is not the right choice when several processes write the same database.

A durable state entity keeps no journal. Only its latest state is stored, as protobuf bytes together with the full name of its message, so it can be unmarshalled back into the right type when the entity restarts.
Each write is an upsert on the persistence id, so the table holds exactly one row per entity.

The store opens the database in write-ahead logging mode with a busy timeout, which is what lets readers run during a write and turns write contention into a short wait.
It either opens its own database handle from a `Config` or runs on a `*sql.DB` you already own.

## Schema

Apply the DDL before starting your application:

```bash
sqlite3 /var/lib/ego/states.db < resources/durablestore_sqlite.sql
```

It creates the `states_store` table:

```sql
CREATE TABLE IF NOT EXISTS states_store
(
    persistence_id  TEXT    PRIMARY KEY,
    version_number  INTEGER NOT NULL,
    state_payload   BLOB    NOT NULL,
    state_manifest  TEXT    NOT NULL,
    timestamp       INTEGER NOT NULL,
    shard_number    INTEGER NOT NULL
);
```

The persistence id is the primary key, which is what makes the write an upsert.
`version_number` is incremented by eGo on every state change and `state_manifest` holds the protobuf message name used to rebuild the state.

## Installation

```bash
go get github.com/tochemey/ego-contrib/durablestore/sqlite@vX.Y.Z
```

## HowTo

### Create a store that owns its database

Give the store a `Config` with the path of the database file. It opens the file on `Connect` and closes it on `Disconnect`:

```go
store := sqlite.NewDurableStore(&sqlite.Config{
    DBPath: "/var/lib/ego/states.db",
})

if err := store.Connect(ctx); err != nil {
    return err
}
defer store.Disconnect(ctx)
```

The file and its directory must be writable by the process. Leave `DBPath` empty to get an in-memory database that lives as long as the store is connected, which suits tests.

`Config` also carries the SQLite and pool settings. Leave a field empty to take its default:

| Field | Default | Meaning |
|---|---|---|
| `JournalMode` | `JournalModeWAL` | Write-ahead logging, which lets readers run during a write |
| `Synchronous` | `SynchronousNormal` | Durable across process crashes, an fsync per checkpoint rather than per commit |
| `BusyTimeout` | 5 seconds | How long a connection waits for the write lock before failing |
| `MaxOpenConnections` | 4 | Largest number of open connections |
| `MaxIdleConnections` | same as open | Connections kept open when idle |
| `ConnMaxLifetime` | 1 hour | Age at which a connection is closed |
| `ConnMaxIdleTime` | 30 minutes | Idle time after which a connection is closed |

`JournalMode` and `Synchronous` are typed, and the package declares a constant for every value SQLite accepts.

### Tune SQLite with custom pragmas

Any other pragma goes in `Pragmas`. Entries there are applied on every connection and override the named settings above when they name the same pragma:

```go
store := sqlite.NewDurableStore(&sqlite.Config{
    DBPath: "/var/lib/ego/states.db",
    Pragmas: map[string]string{
        "cache_size": "-64000", // 64 MiB of page cache per connection
        "temp_store": "MEMORY", // keep temporary tables and indexes in memory
    },
})
```

See the [SQLite pragma reference](https://www.sqlite.org/pragma.html) for the full list.

### Run on a database handle you own

When your application already has a `*sql.DB`, or when several stores must share one, pass it in.
`Connect` then only pings the handle and `Disconnect` leaves it open:

```go
dsn := "file:/var/lib/ego/ego.db?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_txlock=immediate"

db, err := sql.Open("sqlite", dsn)
if err != nil {
    return err
}
defer db.Close()

store := sqlite.NewDurableStoreWithSqlite(db)
if err := store.Connect(ctx); err != nil {
    return err
}
defer store.Disconnect(ctx)
```

Open the handle with the pragmas shown, otherwise concurrent writes fail with a locked database instead of waiting.

The store depends on the `Sqlite` interface, which declares `ExecContext`, `QueryContext` and `PingContext`.
A `*sql.DB` satisfies it as is, and so does any wrapper of your own, for instance one that adds tracing.
`Close` is deliberately absent, so a store never closes a handle it did not open.
The same handle can therefore back the event, snapshot, durable state and offset stores of one application.
Those four modules all declare a package named `sqlite`, so alias them on import when you use more than one:

```go
import (
    durablestore "github.com/tochemey/ego-contrib/durablestore/sqlite"
    offsetstore "github.com/tochemey/ego-contrib/offsetstore/sqlite"
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

`go test ./...` runs the suite against temporary database files. No Docker and no server are needed.
The suite includes a test that writes from eight goroutines at once to confirm the busy timeout absorbs write contention.

From the repository root, `make test/durablestore/sqlite` runs the same suite.
