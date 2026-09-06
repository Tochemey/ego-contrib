# Design Proposal: SQLite + Litestream Event-Sourced Persistence (one instance per actor)

- **Status:** Proposal (draft for review)
- **Module:** `eventstore/sqlite`
- **Scope:** Event-sourced persistence only (`persistence.EventsStore`). Durable state is covered by the sibling proposal in `docs/design/sqlite-litestream-durable-state.md`.
- **Maintainer:** eGo Contrib community

## 1. Motivation

Event-sourced actors in eGo need an append-only journal. Today that means an external database (PostgreSQL, DynamoDB, Cassandra) or in-memory storage (tests/PoC only). For small single-node or edge deployments this is overkill, exactly like the durable-state case.

This proposal applies the same approach as the durable-state store: an embedded SQLite database per actor whose write-ahead log (WAL) is continuously replicated to S3 by the embedded Litestream library. The journal therefore stays local and fast for the hot path, while durability and recoverability come from S3 — with no database server to run.

The hard part that is **specific to event sourcing** is the `EventsStore` interface's cross-actor queries (`PersistenceIDs`, `ShardNumbers`, `GetShardEvents`), which must be reconciled with the "one SQLite instance per actor" model. Section 6 covers this.

## 2. Goals / Non-Goals

### Goals
- Implement the eGo `persistence.EventsStore` interface.
- Append-only event journal per actor in a local SQLite database (1 actor = 1 SQLite file).
- Replicate each actor's WAL to S3 via the embedded Litestream library.
- Restore a single actor's journal from S3 on demand.
- Support the interface's cross-actor queries (`PersistenceIDs`, `ShardNumbers`, `GetShardEvents`) at the target footprint.

### Non-Goals
- **Durable state** (`durablestore`) — covered by the sibling proposal.
- Projection offset persistence (`offsetstore`).
- Snapshot storage (`snapshotstore`).
- Multi-writer / distributed actors. A journal is written by one process at a time.
- Global indexes / analytic queries over all events (e.g. arbitrary cross-actor event scanning at scale). Only the interface's required queries are supported.

## 3. Background

### 3.1 eGo `persistence.EventsStore`
The store must satisfy (mirroring `eventstore/postgres`):

```go
type EventsStore interface {
    Connect(ctx context.Context) error
    Disconnect(ctx context.Context) error
    Ping(ctx context.Context) error

    // Per-actor journal operations
    WriteEvents(ctx context.Context, events []*egopb.Event) error
    DeleteEvents(ctx context.Context, persistenceID string, toSequenceNumber uint64) error
    ReplayEvents(ctx context.Context, persistenceID string, fromSequenceNumber, toSequenceNumber uint64, limit uint64) ([]*egopb.Event, error)
    GetLatestEvent(ctx context.Context, persistenceID string) (*egopb.Event, error)

    // Cross-actor queries
    PersistenceIDs(ctx context.Context, pageSize uint64, pageToken string) ([]string, string, error)
    ShardNumbers(ctx context.Context) ([]uint64, error)
    GetShardEvents(ctx context.Context, shardNumber uint64, offset int64, limit uint64) ([]*egopb.Event, int64, error)
}
```

- `WriteEvents` inserts a batch atomically; sequence numbers are monotonically increasing per actor and unique.
- `DeleteEvents` physically deletes events up to a sequence number (inclusive).
- `ReplayEvents`/`GetLatestEvent` are per-actor reads, ordered by sequence number.
- `PersistenceIDs`, `ShardNumbers`, `GetShardEvents` span actors/shard memberships and are the design challenge (Section 6).

### 3.2 SQLite WAL mode & Litestream embedded library
Same shared foundation as the durable-state proposal (`docs/design/sqlite-litestream-durable-state.md`):
- SQLite in WAL mode (`journal_mode = WAL`, `synchronous = NORMAL`, `busy_timeout`).
- Litestream embedded Go library (`github.com/benbjohnson/litestream`): one `DB` handle + one `Replica` per actor S3 prefix; `db.Sync(ctx)` uploads the WAL; `Replica.Restore(...)` pulls a journal back from S3.
- No daemon, no config file, no embedded binary.

This document only details what is specific to the event journal; shared decisions (sync modes, restore flow, naming/escaping, file layout, credentials) are inherited from the durable-state proposal.

## 4. Design Overview

```
Actor journal (persistenceID = "account-42")
        │
        ▼
┌─────────────────────────┐    WAL      ┌──────────────────────────┐
│  EventsStore (sqlite)   │─────────────▶│  SQLite journal per actor │
│  eventstore/sqlite      │ local r/w   │  data/journals/account-42 │
│  (append-only)          │             │   account-42             │
└─────────────────────────┘             │   account-42-wal          │
                                        └────────────┬─────────────┘
                                                     │  litestream DB.Sync(ctx)
                                                     │  (embedded, per actor)
                                                     ▼
                                        ┌──────────────────────────┐
                                        │  S3 bucket               │
                                        │  s3://ego-journal/       │
                                        │    account-42/           │
                                        └──────────────────────────┘
```

### 4.1 File layout
Identical to the durable-state proposal, under a distinct data dir:

```
dataDir/
  <persistenceID>/
    <persistenceID>.db       # SQLite journal database
    <persistenceID>-wal      # WAL (streamed to S3 by litestream)
    <persistenceID>-shm      # shared-memory index (WAL mode)
```

The directory name **is** the persistenceID (normalized via the same `fileKey()` escaping as the durable-state store). This makes `PersistenceIDs` a cheap directory listing (Section 6).

### 4.2 Schema (per-actor journal table)
Each actor DB contains the same journal table, mirroring `eventstore/postgres/resources/eventstore_postgres.sql`:

```sql
CREATE TABLE IF NOT EXISTS events_store (
    persistence_id   TEXT    NOT NULL,
    sequence_number  INTEGER NOT NULL,
    is_deleted       BOOLEAN NOT NULL DEFAULT 0,
    event_payload    BLOB    NOT NULL,
    event_manifest   TEXT    NOT NULL,
    timestamp        INTEGER NOT NULL,  -- unix epoch milliseconds
    shard_number     INTEGER NOT NULL,
    encryption_key_id TEXT   NOT NULL DEFAULT '',
    is_encrypted     BOOLEAN NOT NULL DEFAULT 0,
    PRIMARY KEY (persistence_id, sequence_number)
);

CREATE INDEX IF NOT EXISTS idx_events_store_shard ON events_store(shard_number);
CREATE INDEX IF NOT EXISTS idx_events_store_timestamp ON events_store(timestamp);
```

- Since each DB holds a single actor's events, the `(persistence_id, sequence_number)` PK degrades to a `sequence_number` uniqueness constraint, which is what we actually want: **append-only, no duplicate sequence numbers** per actor.
- `event_payload` stores the serialized protobuf (`proto.Marshal`), `event_manifest` the full message name, matching the postgres store's row mapping (`row.go`).

## 5. Per-actor journal operations

All per-actor operations target exactly one actor's SQLite file via the cached `actorDB(ctx, pid)` handle (open + migrate + restore-from-S3-if-missing, same as the durable-state store).

### 5.1 WriteEvents
- Open a single SQLite transaction (`BEGIN IMMEDIATE`), insert all events with `INSERT OR ... `; any duplicate `sequence_number` violates the PK and aborts the transaction — surfacing a conflicting-sequence error to eGo.
- Commit, then sync per `SyncMode`:
  - `SyncOnWrite`: `lsDB.Sync(ctx)` after commit. Because a batch amortizes many events over one S3 round-trip, this is cheap relative to the durable-state single-write case.
  - `SyncInterval`: commit locally and let the background syncer drain the WAL; final sync on `Disconnect`.
- No batching-by-parameter-limit is needed: SQLite's variable limit (default 32766 in modern versions) is far above typical eGo event batches, and we chunk only if a batch exceeds it.

### 5.2 DeleteEvents
- Physical `DELETE FROM events_store WHERE sequence_number <= ?` on that actor's DB, in a transaction.
- Deletes produce WAL traffic just like inserts; the replica handles them (snapshot + log). Note the retention interplay in Section 7: deleted generations still consume S3 until the replica's snapshot consolidates.

### 5.3 ReplayEvents / GetLatestEvent
- Per-actor `SELECT ... WHERE sequence_number BETWEEN ? AND ? ORDER BY sequence_number ASC LIMIT ?`; rows unmarshalled with the shared `toProto` manifest pattern.
- `GetLatestEvent` = same query `ORDER BY sequence_number DESC LIMIT 1`, returns `nil` when absent.

## 6. Cross-actor queries (the per-actor model's special case)

These methods span multiple actor DBs. Because a journal is a directory per actor, we implement them without a global index at the target footprint:

### 6.1 PersistenceIDs
- **Directory listing, not SQL:** enumerate subdirectories of `dataDir`, apply `fileKey` decoding, sort, paginate with `pageToken` as a keyset (`> pageToken`).
- Zero DB reads; scales with actor count as a filesystem walk. This is the cleanest win of the per-actor layout.

### 6.2 ShardNumbers
- **Fan-out with caching:** for each actor DB run `SELECT DISTINCT shard_number FROM events_store`, union the results.
- Cache the result (invalidated on any `WriteEvents`/`DeleteEvents`/restore) to avoid rescanning on every call. A single actor normally maps to one shard, so the cache is small.

### 6.3 GetShardEvents
- **Fan-out + merge:** per actor DB run `SELECT ... WHERE shard_number = ? AND timestamp > ? ORDER BY timestamp ASC LIMIT ?`; merge the per-actor results, sort by timestamp, apply the global `limit`, return the last timestamp as the next offset.
- Cost is O(#actors × per-actor scan). Acceptable for small/edge footprints (tens to low-hundreds of actors per process) and for eGo's projection/reminder use of shard polling. At larger scale this is the first thing to revisit (see Open Questions #4: a shard-index DB as a follow-up).

## 7. Litestream integration (embedded library)

Inherited from the durable-state proposal, applied to the journal:
- One `litestream.Replica` per actor S3 prefix: `s3://bucket/ego-journal/<pid>/`.
- `SyncOnWrite` (default) or `SyncInterval` background sync via `Config`.
- `Replica.Restore(ctx, lsDB, dest, tmpPath)` on first access when the local DB is absent and a replica exists.
- Replica `SnapshotInterval` / `RetainDuration` configurable; retention bounds S3 cost. **Note for journals:** `DeleteEvents` does not shrink the replica; retention is what eventually reclaims space, so set `RetainDuration` with replay-window needs in mind.

## 8. Concurrency & Consistency

- **Per-actor serialization:** one writer at a time per actor DB (`SetMaxOpenConns(1)` + `BEGIN IMMEDIATE`), matching eGo's sequential per-actor event production.
- **Append-only integrity:** `(persistence_id, sequence_number)` PK rejects duplicate sequence numbers; a write batch is atomic (all-or-nothing), so a partially-synced batch never lands.
- **Cross-actor isolation:** different actors never share a lock or file.
- **Monotonicity (optional):** compare the batch's first sequence number against `GetLatestEvent` before insert to fail fast on out-of-order writers; PK already provides the hard guarantee.

## 9. Trade-offs: one SQLite instance per actor (event sourcing)

### Advantages
- Append-only per-actor journals never contend; no global write lock.
- Granular restore/delete of one actor's journal in S3.
- `PersistenceIDs` is a free directory listing.
- A corrupted journal only affects one actor; replay of one actor never scans others.

### Disadvantages / risks
- Cross-actor queries (`GetShardEvents`, `ShardNumbers`) require fan-out across actor DBs — the interface was designed for a shared table. Costs grow with actor count.
- Many files/replicas: N actors → N DB files + N S3 prefixes; S3 object counts multiply.
- `DeleteEvents` space in S3 is reclaimed only by retention, not immediately.
- File descriptor pressure at scale — LRU cache of actor DBs mitigates (same as durable-state).

## 10. Config

```go
type Config struct {
    DataDir          string        // root directory holding per-actor journals (required)
    BucketURL        string        // S3 base URL, e.g. "s3://my-bucket/ego-journal" (required)
    Endpoint         string        // optional custom S3 endpoint (MinIO/compatible stores)
    Region           string        // optional S3 region override

    SyncMode         SyncMode      // SyncOnWrite (default) or SyncInterval
    SyncInterval     time.Duration // interval for batched background sync (0 when SyncOnWrite)
    SnapshotInterval time.Duration // litestream replica snapshot interval (default 24h)
    RetainDuration   time.Duration // litestream replica retention (default 30d)
    MaxOpenDBs       int           // LRU cap for concurrently open actor DBs (default 128)
}
```

`NewEventsStore(cfg)` returns an `*EventsStore` implementing `persistence.EventsStore`.

## 11. Implementation sketch (Go)

```go
package sqlite

type actorJournal struct {
    sql     *sql.DB
    replica *litestream.Replica
    lsDB    *litestream.DB
}

type EventsStore struct {
    cfg       *Config
    mu        sync.Mutex
    journals  map[string]*actorJournal // LRU keyed by persistenceID
    shards    cached[[]uint64]         // ShardNumbers cache, invalidated on write/delete
    syncer    *syncer
    connected bool
}

func (s *EventsStore) Connect(ctx context.Context) error {
    // mkdir -p DataDir; verify bucket reachable
}

func (s *EventsStore) Disconnect(ctx context.Context) error {
    // stop syncer, final Sync() per journal, checkpoint WAL, close
}

func (s *EventsStore) WriteEvents(ctx context.Context, events []*egopb.Event) error {
    adb := s.journal(ctx, events[0].GetPersistenceId())
    // BEGIN IMMEDIATE; INSERT ... (chunked if > max vars); COMMIT
    if s.cfg.SyncMode == SyncOnWrite {
        return adb.lsDB.Sync(ctx)
    }
    s.invalidateShardCache()
    return nil
}

func (s *EventsStore) ReplayEvents(ctx, pid string, from, to, limit uint64) ([]*egopb.Event, error) {
    adb := s.journal(ctx, pid) // may restore from S3
    // SELECT ... WHERE sequence_number BETWEEN from AND to ORDER BY seq ASC LIMIT limit
}

func (s *EventsStore) GetShardEvents(ctx, shard uint64, offset int64, limit uint64) ([]*egopb.Event, int64, error) {
    // fan-out: per journal SELECT ... WHERE shard_number=? AND timestamp>? ORDER BY timestamp ASC LIMIT limit
    // merge + sort by timestamp + apply global limit; nextOffset = last timestamp
}
```

## 12. Module layout

```
eventstore/sqlite/
  events_store.go   # EventsStore implementation
  journal.go        # per-actor journal: open, migrate, replica wiring, restore
  crossactor.go     # PersistenceIDs / ShardNumbers / GetShardEvents
  syncer.go         # background WAL syncer (SyncInterval mode)
  config.go         # Config + defaults
  row.go            # row <-> egopb.Event mapping
  resources/        # (schema embedded or applied on open)
  README.md
  Earthfile         # module build (mirrors sibling modules)
  go.mod            # module go.mod
```

The module ships as `github.com/tochemey/ego-contrib/eventstore/sqlite`.

## 13. Testing strategy

- **Unit tests:** append-only PK violation on duplicate sequence numbers, `WriteEvents` atomicity, `DeleteEvents` boundary (inclusive), `ReplayEvents` range ordering, `GetLatestEvent` on empty journal returns nil.
- **Cross-actor tests:** `PersistenceIDs` pagination via directory listing, `ShardNumbers` union + cache invalidation, `GetShardEvents` merge/sort/limit correctness with multiple actors in the same shard.
- **Integration (Testcontainers-Go + MinIO, matching sibling modules):** WAL produced and synced to MinIO (`db.Sync`); `Replica.Restore` recreates an actor's full journal after local files are deleted; retention/snapshot behavior honored.
- **Restart/replay:** write events, "lose" local data, restore from MinIO, verify full replay returns the same ordered events.

## 14. Operational notes

- Same as durable-state: no daemon; `SyncOnWrite` gives the smallest RPO; `SyncInterval` trades durability window for throughput; S3 credentials via the standard AWS credential chain.
- Set `RetainDuration` to cover the longest expected replay/audit window — deletion does not immediately free S3 space.
- `GetShardEvents` cost grows with actor count; monitor if the process hosts many actors.

## 15. Open questions

1. Driver choice: `modernc.org/sqlite` (pure Go) vs `mattn/go-sqlite3` (CGO) — shared decision with the durable-state module.
2. Should `DeleteEvents` physically delete rows (postgres parity) or tombstone them (`is_deleted`) to preserve replay history? Postgres store deletes; SQLite gives us cheap tombstones if auditability matters.
3. Shard cache invalidation: invalidate on every write vs a short TTL — a hot writer could otherwise thrash the cache.
4. Scale ceiling for the fan-out `GetShardEvents` — if projections need high throughput across many actors, a follow-up shard-index SQLite DB (or shared journal DB per shard) may be warranted.
5. Reuse of the shared litestream/sqlite plumbing between `durablestore/sqlite` and `eventstore/sqlite` (an internal `sqlitekit` package) vs duplication, given the two proposals share file layout, sync modes, restore, and LRU logic.