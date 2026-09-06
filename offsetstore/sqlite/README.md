# Offset Store (SQLite)

## Overview

This module persists the offsets of [eGo](https://github.com/Tochemey/ego) projections in SQLite.
It implements `github.com/tochemey/ego/v4/offsetstore.OffsetStore` on top of `modernc.org/sqlite`, a pure Go driver, so the module builds without cgo and cross-compiles like any other Go package.

SQLite keeps everything in a single file and needs no server, which suits embedded deployments, edge nodes, command line tools and tests.
It allows one writer at a time, so it is not the right choice when several processes write the same database.

A projection reads the events of each shard in order and records how far it got.
That mark is the offset, and it is the timestamp of the last event the projection handled.
Storing it is what lets a projection resume where it stopped instead of reprocessing a whole journal after a restart.

Offsets are kept per projection and per shard, so the projections of one application advance independently and a projection can progress at a different pace on each shard.

The store opens the database in write-ahead logging mode with a busy timeout, which is what lets readers run during a write and turns write contention into a short wait.
It either opens its own database handle from a `Config` or runs on a `*sql.DB` you already own.

## Schema

Apply the DDL before starting your application:

```bash
sqlite3 /var/lib/ego/offsets.db < resources/offsetstore_sqlite.sql
```

It creates the `offsets_store` table:

```sql
CREATE TABLE IF NOT EXISTS offsets_store
(
    projection_name TEXT    NOT NULL,
    shard_number    INTEGER NOT NULL,
    current_offset  INTEGER NOT NULL,
    timestamp       INTEGER NOT NULL,
    PRIMARY KEY (projection_name, shard_number)
);
```

The primary key makes a projection name and a shard number unique together, so each write is an upsert and the table never holds more than one row per pair.
`current_offset` is the position reached and `timestamp` records when it was last written.

## Installation

```bash
go get github.com/tochemey/ego-contrib/offsetstore/sqlite@vX.Y.Z
```

## HowTo

### Create a store that owns its database

Give the store a `Config` with the path of the database file. It opens the file on `Connect` and closes it on `Disconnect`:

```go
store := sqlite.NewOffsetStore(&sqlite.Config{
    DBPath: "/var/lib/ego/offsets.db",
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
store := sqlite.NewOffsetStore(&sqlite.Config{
    DBPath: "/var/lib/ego/offsets.db",
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

store := sqlite.NewOffsetStoreWithSqlite(db)
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
    eventstore "github.com/tochemey/ego-contrib/eventstore/sqlite"
    offsetstore "github.com/tochemey/ego-contrib/offsetstore/sqlite"
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

`go test ./...` runs the suite against temporary database files. No Docker and no server are needed.
The suite includes a test that writes from eight goroutines at once to confirm the busy timeout absorbs write contention.

From the repository root, `make test/offsetstore/sqlite` runs the same suite.
