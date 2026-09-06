# Events Store (SQLite)

## Overview

This module persists [eGo](https://github.com/Tochemey/ego) event journals in SQLite.
It implements `github.com/tochemey/ego/v4/persistence.EventsStore` on top of `modernc.org/sqlite`, a pure Go driver, so the module builds without cgo and cross-compiles like any other Go package.

SQLite keeps the whole journal in a single file and needs no server, which suits embedded deployments, edge nodes, command line tools and tests.
It allows one writer at a time, so it is not the right choice when several processes write the same journal.

Each event is stored as protobuf bytes together with the full name of its message, so it can be unmarshalled back into the right type on replay.
Writes go through a transaction and are batched, 500 events per statement by default, well below the 32766 bound variables SQLite accepts in one statement.

The store opens the database in write-ahead logging mode with a busy timeout, which is what lets readers run during a write and turns write contention into a short wait.
It either opens its own database handle from a `Config` or runs on a `*sql.DB` you already own.

## Schema

Apply the DDL before starting your application:

```bash
sqlite3 /var/lib/ego/events.db < resources/eventstore_sqlite.sql
```

It creates the `events_store` table:

```sql
CREATE TABLE IF NOT EXISTS events_store(
    persistence_id TEXT NOT NULL,
    sequence_number INTEGER NOT NULL,
    is_deleted INTEGER DEFAULT 0 NOT NULL,
    event_payload BLOB NOT NULL,
    event_manifest TEXT NOT NULL,
    timestamp INTEGER NOT NULL,
    shard_number INTEGER NOT NULL,
    encryption_key_id TEXT DEFAULT '' NOT NULL,
    is_encrypted INTEGER DEFAULT 0 NOT NULL,
    PRIMARY KEY (persistence_id, sequence_number)
);

CREATE INDEX IF NOT EXISTS idx_events_store_timestamp ON events_store(timestamp);
CREATE INDEX IF NOT EXISTS idx_events_store_shard ON events_store(shard_number);
```

The primary key makes a persistence id and a sequence number unique together.
The two indexes serve the shard queries eGo projections run.
Booleans are stored as integers, which is how SQLite represents them.

## Installation

```bash
go get github.com/tochemey/ego-contrib/eventstore/sqlite@vX.Y.Z
```

## HowTo

### Create a store that owns its database

Give the store a `Config` with the path of the database file. It opens the file on `Connect` and closes it on `Disconnect`:

```go
store := sqlite.NewEventsStore(&sqlite.Config{
    DBPath: "/var/lib/ego/events.db",
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
store := sqlite.NewEventsStore(&sqlite.Config{
    DBPath: "/var/lib/ego/events.db",
    Pragmas: map[string]string{
        "cache_size": "-64000",    // 64 MiB of page cache per connection
        "mmap_size":  "268435456", // map 256 MiB of the file into memory
        "temp_store": "MEMORY",    // keep temporary tables and indexes in memory
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

store := sqlite.NewEventsStoreWithSqlite(db)
if err := store.Connect(ctx); err != nil {
    return err
}
defer store.Disconnect(ctx)
```

Open the handle with the pragmas shown, otherwise concurrent writes fail with a locked database instead of waiting.
`_txlock=immediate` makes every transaction take the write lock when it begins, so a second writer waits for the busy timeout rather than being refused outright.

The store depends on the `Sqlite` interface, which declares `ExecContext`, `QueryContext`, `BeginTx` and `PingContext`.
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

`go test ./...` runs the suite against temporary database files. No Docker and no server are needed.
The suite includes a test that writes from eight goroutines at once to confirm the busy timeout absorbs write contention.

From the repository root, `make test/eventstore/sqlite` runs the same suite.
