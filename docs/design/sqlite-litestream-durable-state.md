# Design Proposal: SQLite + Litestream Durable State Store (one instance per actor)

- **Status:** Proposal (draft for review)
- **Module:** `durablestore/sqlite`
- **Scope:** Durable state only (`persistence.StateStore`). Event sourcing / projection offset persistence is explicitly **out of scope**.
- **Maintainer:** eGo Contrib community

## 1. Motivation

eGo durable state currently requires an external database (PostgreSQL, DynamoDB, Cassandra) or is held only in memory. For small, single-node deployments — edge devices, embedded runtimes, development, or workloads with modest state per actor — a full external database is overkill:

- provisioning and operational burden
- network dependency / latency
- no easy "state lives with the process" story

SQLite gives us a zero-ops, embedded, transactional datastore that lives on the local filesystem. By streaming its write-ahead log (WAL) to S3 with [Litestream](https://litestream.io), we recover the durability of a remote store without running a database server: local reads/writes stay local and fast, while the WAL is continuously replicated to object storage for disaster recovery and restart-from-backup.

The core design decision in this proposal is **one SQLite database (and therefore one Litestream replica stream) per actor** rather than a single shared database for all actors.

## 2. Goals / Non-Goals

### Goals
- Implement the eGo `persistence.StateStore` interface (Connect, Disconnect, Ping, WriteState, GetLatestState).
- Store durable state in a local SQLite database.
- Replicate each SQLite WAL to S3 via Litestream so state survives node loss and can be restored.
- Support 1 actor = 1 SQLite file = 1 replica (Litestream stream).

### Non-Goals
- **Event sourcing persistence** (`eventstore`). Journal entries are not covered.
- Projection offset persistence (`offsetstore`).
- Snapshot storage (`snapshotstore`).
- Multi-writer / distributed coordination across nodes for a single actor. This store is intended for a local actor running in one process at a time.
- Sharding/partitioning logic beyond the per-actor file split.

## 3. Background

### 3.1 eGo `persistence.StateStore`
The store must satisfy (mirroring the existing Postgres store in `durablestore/postgres`):

```go
type StateStore interface {
    Connect(ctx context.Context) error
    Disconnect(ctx context.Context) error
    Ping(ctx context.Context) error
    WriteState(ctx context.Context, state *egopb.DurableState) error
    GetLatestState(ctx context.Context, persistenceID string) (*egopb.DurableState, error)
}
```

- `WriteState` is an **upsert** keyed on `persistence_id` (`version_number` is monotonically increasing per actor).
- `GetLatestState` returns the single latest state or `nil` when absent.

### 3.2 SQLite WAL mode
SQLite supports a write-ahead log: all writes go to a `-wal` file which is later checkpointed back into the main database file. This is exactly the artifact Litestream replicates to S3. Config:
- `PRAGMA journal_mode = WAL`
- `PRAGMA synchronous = NORMAL`
- `PRAGMA busy_timeout`

### 3.3 Litestream
Litestream is an open-source continuous replication tool for SQLite. It ships both a CLI daemon and, importantly for this proposal, an **embedded Go library** (`github.com/benbjohnson/litestream`) that can be linked directly into a Go program. The library:
- manages a `DB` handle per SQLite file with one or more `Replica`s (each pointed at an S3 prefix),
- `Sync()`s the WAL to the replicas on demand (snapshot + incremental log upload),
- `Restore()`s a database from a replica into a local file,
- keeps the snapshot/log retention policy on the replica.

No external process, config file, or embedded binary is required. Each `DB` (SQLite file) maps one-to-one to a `Replica` (S3 prefix).

## 4. Design Overview

```
Actor (persistenceID = "account-42")
        │
        ▼
┌─────────────────────────┐     WAL      ┌──────────────────────────┐
│  StateStore (sqlite)    │──────────────▶│  SQLite file per actor   │
│  durablestore/sqlite    │   local r/w   │  data/actors/account-42  │
└─────────────────────────┘               │   account-42            │
                                          │   account-42-wal         │
                                          └────────────┬─────────────┘
                                                       │  litestream DB.Sync(ctx)
                                                       │  (embedded, per actor)
                                                       ▼
                                          ┌──────────────────────────┐
                                          │  S3 bucket               │
                                          │  s3://ego-state/actors/  │
                                          │    account-42/           │
                                          │      account-42/...      │
                                          └──────────────────────────┘
```

### 4.1 File layout
One directory holds all per-actor databases. Each actor maps to:

```
dataDir/
  <persistenceID>/
    <persistenceID>.db       # SQLite database
    <persistenceID>-wal      # WAL file (streamed to S3 by litestream)
    <persistenceID>-shm      # shared-memory index (WAL mode)
```

Using a per-actor **directory** (rather than a flat `<pid>.db` file) keeps each actor's replica self-contained and gives Litestream a clean S3 prefix per actor.

### 4.2 Naming / mapping
- `persistenceID` may contain characters unsafe for filesystem/S3 keys. A deterministic escaping function (`fileKey(persistenceID)`) normalizes the name (e.g. base64url-encoded or hex of the SHA-256) while preserving a stable, reversible mapping. A reverse index is **not** needed because Litestream restore is keyed by persistenceID directly.

### 4.3 Connection model
Each SQLite database is opened lazily on first `WriteState`/`GetLatestState` for that `persistenceID`, and cached in an in-memory map (`map[persistenceID]*sqlDB`). The `StateStore` does **not** open one connection per call; it keeps a cached handle per actor.

- WAL mode allows a single writer plus concurrent readers; a mutex or `database/sql` with `SetMaxOpenConns(1)` per DB serializes writes per actor, matching eGo's per-actor sequential state updates.
- `busy_timeout` prevents immediate `SQLITE_BUSY` on contention.

### 4.4 Schema (per-actor table)
Each SQLite DB contains a single-row-per-actor table:

```sql
CREATE TABLE IF NOT EXISTS states_store (
    persistence_id  TEXT PRIMARY KEY,
    version_number  INTEGER NOT NULL,
    state_payload   BLOB    NOT NULL,
    state_manifest  TEXT    NOT NULL,
    timestamp       INTEGER NOT NULL,  -- unix epoch milliseconds
    shard_number    INTEGER NOT NULL
);
```

This mirrors the existing Postgres schema (`durablestore/postgres/resources/durablestore_postgres.sql`) for consistency.

## 5. Litestream Integration (embedded library)

### 5.1 In-process replication
The module links the Litestream **embedded Go library** (`github.com/benbjohnson/litestream`) directly. There is no daemon, no generated config file, and no external binary to deploy. Each actor DB is wrapped in a `*litestream.DB` with exactly one `*litestream.Replica` pointing at that actor's S3 prefix (one SQLite instance per actor = one replica stream).

```
actorDB (wraps *sql.DB + *litestream.DB)
   │  WriteState → INSERT ... ON CONFLICT
   │  db.Sync(ctx)             ── upload WAL (snapshot + log) ──▶ s3://ego-state/actors/<pid>/
   ▼
   litestream.Replica (retention, snapshot interval)
```

### 5.2 Per-actor replica construction
```go
lsDB := litestream.NewDB(dbPath)
replica, err := litestream.NewReplica(lsDB, "s3", "s3://bucket/ego-state/actors/<pid>")
// replica.SnapshotInterval, replica.RetainDuration configured from Config
lsDB.Replicas = append(lsDB.Replicas, replica)
```

- `NewReplica` accepts an S3 URL, so region/endpoint options (needed for MinIO/compatible stores) can be passed via URL options.
- Credentials resolve through the standard AWS credential chain (env, shared config, IAM role), matching Litestream's own behavior — nothing new to configure.
- Opening the underlying SQLite in WAL mode is required before Litestream can replicate; the module enforces `PRAGMA journal_mode = WAL`.

### 5.3 Sync semantics
Durability is achieved by calling `lsDB.Sync(ctx)` after writes. Two modes, exposed on `Config`:

- **Sync-on-write (default):** `WriteState` performs the SQL upsert and then `db.Sync(ctx)` before returning. Smallest RPO (a committed write is already in S3); cost is one S3 round-trip per write.
- **Batched background sync:** `SyncInterval > 0` spawns a per-store goroutine that periodically calls `Sync()`; `WriteState` returns after the local commit and the background loop drains the WAL. Higher throughput, RPO bounded by `SyncInterval`, and `Disconnect` performs a final synchronous `Sync()`.

Because eGo actor state updates are naturally serialized per actor, syncs for different actors happen independently and never contend.

### 5.4 Restore flow
Restore uses the replica's `Restore` method against the actor's S3 prefix:

```
if !exists(localDB) && replicaExistsInS3:
    replica.Restore(ctx, lsDB, dbPath, tmpPath)  // pull snapshot + generations into local file
```

- `Replica.Restore(ctx, db, dest, tmpPath)` is the library's restore entry point; it copies the latest snapshot and any subsequent generations into `dest`.

- Triggered lazily inside `actorDB(ctx, pid)` on first access when the local DB file is absent.
- If no replica exists in S3, the actor starts fresh (normal first-run for a new persistenceID).
- A synchronous restore blocks `GetLatestState`/`WriteState` for that actor only; other actors' DBs are unaffected.

## 6. Concurrency & Consistency

- **Per-actor serialization:** Writes to one actor's DB are serialized (single writer, `MaxOpenConns(1)`), matching eGo's model where a single actor writes its own durable state sequentially.
- **Cross-actor isolation:** Different actors touch different files, so there is zero cross-actor lock contention — a key benefit of "one SQLite instance per actor."
- **Upsert semantics:** `INSERT ... ON CONFLICT(persistence_id) DO UPDATE`, mirroring the Postgres store. `WriteState` is idempotent.
- **Version guard (optional):** since eGo writes versions in order, an optional check that the incoming `version_number` is monotonic can be added to reject stale writes.

## 7. Trade-offs: one SQLite instance per actor

### Advantages
- **Isolation & no contention:** each actor's writes are independent; no global write lock on a single shared DB.
- **Granular restore:** recover a single actor's state from S3 without downloading others.
- **Granular deletion:** delete/GC one actor's S3 prefix independently.
- **WAL checkpoint isolation:** a large actor's WAL does not force checkpointing of others.
- **Simple failure domain:** a corrupted DB only affects one actor.

### Disadvantages / risks
- **Many files / many replicas:** a system with N actors yields N DB files and N litestream `Replica` handles, so S3 object counts multiply.
- **File descriptor pressure:** N open SQLite files if all actors are active simultaneously. Mitigate with an LRU cache that closes idle DB handles.
- **Not suited for very high actor counts in a single process** — the per-file overhead (and Litestream per-replica snapshotting) becomes non-trivial beyond thousands of actors. Acceptable for the target small/edge footprint; a shared-DB mode could be a follow-up.

## 8. Config

```go
type Config struct {
    DataDir          string        // root directory holding per-actor DBs (required)
    BucketURL        string        // S3 base URL, e.g. "s3://my-bucket/ego-state" (required)
    Endpoint         string        // optional custom S3 endpoint (MinIO/compatible stores)
    Region           string        // optional S3 region override

    SyncMode         SyncMode      // SyncOnWrite (default) or SyncInterval
    SyncInterval     time.Duration // interval for batched background sync (0 when SyncOnWrite)
    SnapshotInterval time.Duration // litestream replica snapshot interval (default 24h)
    RetainDuration   time.Duration // litestream replica retention (default 30d)
    MaxOpenDBs       int           // LRU cap for concurrently open actor DBs (default 128)
}
```

`NewDurableStore(cfg)` returns a `*DurableStore` implementing `persistence.StateStore`.

## 9. Implementation Sketch (Go)

```go
package sqlite

import "github.com/benbjohnson/litestream"

type actorDB struct {
    sql      *sql.DB      // driver handle (modernc.org/sqlite or mattn/go-sqlite3)
    replica  *litestream.Replica
    lsDB     *litestream.DB
}

type DurableStore struct {
    cfg       *Config
    mu        sync.Mutex
    actors    map[string]*actorDB  // LRU cache keyed by persistenceID
    syncer    *syncer              // background goroutine when SyncInterval > 0
    connected bool
}

func (s *DurableStore) Connect(ctx context.Context) error {
    // mkdir -p DataDir; ensure per-actor replicas reachable (bucket check)
}

func (s *DurableStore) Disconnect(ctx context.Context) error {
    // stop syncer, final Sync() on every actor, checkpoint WAL, close lsDBs
}

func (s *DurableStore) Ping(ctx context.Context) error {
    // ensure connected, touch a lightweight query
}

func (s *DurableStore) WriteState(ctx context.Context, st *egopb.DurableState) error {
    adb := s.actorDB(ctx, st.GetPersistenceId())  // open + migrate + restore-if-missing
    // INSERT ... ON CONFLICT DO UPDATE
    if s.cfg.SyncMode == SyncOnWrite {
        return adb.lsDB.Sync(ctx)                  // upload WAL to S3 before returning
    }
    return nil                                     // background syncer drains WAL
}

func (s *DurableStore) GetLatestState(ctx context.Context, id string) (*egopb.DurableState, error) {
    adb := s.actorDB(ctx, id)                      // may trigger Restore from S3
    // SELECT ... ; return nil when no row
}
```

- `actorDB(ctx, pid)` opens the SQLite file, sets WAL pragmas, builds the litestream `Replica`, and restores from S3 when the local file is absent and a replica exists.
- Driver: `modernc.org/sqlite` (pure-Go, no CGO) or `mattn/go-sqlite3` (CGO). Prefer `modernc.org/sqlite` to keep cross-compilation simple; keep this as a decision point.
- Row unmarshalling reuses the same `ToDurableState` / manifest pattern from `durablestore/postgres/row.go`.

## 10. Module layout

```
durablestore/sqlite/
  durable_store.go   # StateStore implementation
  actor.go           # per-actor DB: open, migrate, litestream replica wiring
  syncer.go          # background WAL syncer (SyncInterval mode)
  config.go          # Config + defaults
  row.go             # row <-> egopb.DurableState mapping
  resources/         # (schema embedded or applied on open)
  README.md
  Earthfile          # module build (mirrors sibling modules)
  go.mod             # module go.mod
```

The module ships as `github.com/tochemey/ego-contrib/durablestore/sqlite`.

## 11. Testing strategy

- **Unit tests:** upsert semantics, `GetLatestState` on empty DB returns nil, per-actor isolation (two actors write independently), manifest round-trip.
- **Integration tests with Testcontainers-Go** (matching sibling modules) using a SQLite-in-container and a MinIO container acting as the S3 endpoint:
  - WAL is produced; `db.Sync` uploads snapshot + generations to MinIO.
  - Restore-from-S3 (`Replica.Restore`) recreates state after local files are deleted.
- **Retention/snapshot behavior:** assert snapshot interval and retention are honored against MinIO.

## 12. Operational notes

- No separate daemon to run: replication is in-process. `SyncOnWrite` mode makes every committed write durable in S3 before `WriteState` returns.
- In `SyncInterval` mode, a crash may lose up to `SyncInterval` of writes; choose the mode to match the required RPO.
- Back up the S3 bucket itself for extra safety (Litestream is not a substitute for a full backup strategy).
- Local DB files are ephemeral; on a fresh node, restore happens automatically from S3 on first access.
- S3 credentials come from the standard AWS credential chain (env vars, shared config, IAM role) — configure the same way as any AWS SDK consumer.

## 13. Open questions

1. Driver choice: `modernc.org/sqlite` (pure Go) vs `mattn/go-sqlite3` (CGO) — affects cross-compilation and the module's `go.mod`/Earthfile.
2. Should `WriteState` reject out-of-order versions (monotonicity guard) or trust eGo to sequence writes?
3. Default `SyncMode`: `SyncOnWrite` (per-write S3 round-trip, smallest RPO) vs `SyncInterval` (batched, higher throughput). Validate per-write S3 latency is acceptable for eGo's write path before committing to the default.
4. Feasibility of the per-actor model at target scale — need a rough upper bound on concurrent actors per process before committing. A `litestream.DB` handle per actor adds memory/goroutine overhead on top of the SQLite file handles.
5. Whether restore-on-first-access should be synchronous (blocks `GetLatestState`) or trigger a background restore and return "not found" until complete.
