# Snapshot Store (SQLite)

## Overview

This module persists the snapshots of [eGo](https://github.com/Tochemey/ego) event-sourced entities in SQLite.
It implements `github.com/tochemey/ego/v4/persistence.SnapshotStore` on top of `modernc.org/sqlite`, a pure Go driver, so the module builds without cgo and cross-compiles like any other Go package.

SQLite keeps everything in a single file and needs no server, which suits embedded deployments, edge nodes, command line tools and tests.
It allows one writer at a time, so it is not the right choice when several processes write the same database.

A snapshot is the state of an entity at a given sequence number.
Recovering an entity from its latest snapshot and replaying only the events written after it is far cheaper than replaying a whole journal.
The state is stored as protobuf bytes together with the full name of its message, so it can be unmarshalled back into the right type.

The store opens the database in write-ahead logging mode with a busy timeout, which is what lets readers run during a write and turns write contention into a short wait.
It either opens its own database handle from a `Config` or runs on a `*sql.DB` you already own.

## Schema

Apply the DDL before starting your application:

```bash
sqlite3 /var/lib/ego/snapshots.db < resources/snapshotstore_sqlite.sql
```

It creates the `snapshots_store` table:

```sql
CREATE TABLE IF NOT EXISTS snapshots_store(
    persistence_id TEXT NOT NULL,
    sequence_number INTEGER NOT NULL,
    state_payload BLOB NOT NULL,
    state_manifest TEXT NOT NULL,
    timestamp INTEGER NOT NULL,
    encryption_key_id TEXT NOT NULL DEFAULT '',
    is_encrypted INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (persistence_id, sequence_number)
);
```

The primary key makes a persistence id and a sequence number unique together, so an entity keeps several snapshots and writing the same sequence number twice replaces the row instead of failing.
The encryption columns record whether the payload was encrypted and under which key, when eGo is configured with an encryptor.
Booleans are stored as integers, which is how SQLite represents them.

## Installation

```bash
go get github.com/tochemey/ego-contrib/snapshotstore/sqlite@vX.Y.Z
```

## HowTo

### Create a store that owns its database

Give the store a `Config` with the path of the database file. It opens the file on `Connect` and closes it on `Disconnect`:

```go
store := sqlite.NewSnapshotStore(&sqlite.Config{
    DBPath: "/var/lib/ego/snapshots.db",
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
store := sqlite.NewSnapshotStore(&sqlite.Config{
    DBPath: "/var/lib/ego/snapshots.db",
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

store := sqlite.NewSnapshotStoreWithSqlite(db)
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
    snapshotstore "github.com/tochemey/ego-contrib/snapshotstore/sqlite"
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

`go test ./...` runs the suite against temporary database files. No Docker and no server are needed.
The suite includes a test that writes from eight goroutines at once to confirm the busy timeout absorbs write contention.

From the repository root, `make test/snapshotstore/sqlite` runs the same suite.
