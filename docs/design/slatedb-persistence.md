# Design Proposal: SlateDB-Backed Persistence Stores (Durable State, Events, Offsets)

- **Status:** Proposal (draft for review)
- **Module:** `durablestore/slatedb`, `eventstore/slatedb`, `offsetstore/slatedb`
- **Binding:** [`slatedb.io/slatedb-go/uniffi`](https://pkg.go.dev/slatedb.io/slatedb-go/uniffi) (v0.16.0), imported as `slatedb`
- **Maintainer:** eGo Contrib community

## 1. Motivation

eGo persistence today is served by two families: external databases (PostgreSQL, DynamoDB, Cassandra) or in-memory stores (tests/PoC only). There is a gap for **single-node / edge / small deployments** that want real durability without operating a database server. The sibling SQLite+Litestream proposals fill that gap with an embedded SQLite file per actor whose WAL is replicated to S3.

[SlateDB](https://slatedb.io) offers an alternative that is arguably a better fit for eGo's write pattern: it is an **embedded LSM store whose WAL, manifest, and SSTs all live in object storage** (S3, GCS, Azure Blob, MinIO/Tigris, or in-memory). Unlike SQLite+Litestream, there is no separate replication step to design, operate, or tune — durability and recoverability are **inherent** to the store. The Go binding (`slatedb-go/uniffi`, a UniFFI wrapper over the Rust core, v0.16.0) exposes this directly from Go, including a zero-AWS `memory:///` object store for local/embedded runs and an S3-compatible path for real durability.

This proposal designs a **shared SlateDB persistence layer** that implements three eGo interfaces with one embedded store:

- `persistence.StateStore` — durable state (`durablestore/slatedb`)
- `persistence.EventsStore` — event journals (`eventstore/slatedb`)
- `persistence.OffsetStore` — projection offsets (`offsetstore/slatedb`)

## 2. Goals / Non-Goals

### Goals
- Implement `persistence.StateStore`, `persistence.EventsStore`, and `persistence.OffsetStore` on top of the SlateDB Go binding.
- Support two modes from one codebase:
  - **Embedded / local:** `ObjectStoreResolve("memory:///")` — zero network, zero AWS; for edge, dev, and small single-node workloads.
  - **Object-store-backed:** S3 / GCS / Azure / MinIO via `ObjectStoreResolve(url)` or `ObjectStoreFromEnv` — durable, recoverable, no DB server.
- Keep durability semantics explicit and simple: writes are acknowledged after the SlateDB durability hook (`WriteHandle.AwaitDurable`) or a flush, matching the chosen durability mode.
- One shared internal package for the common SlateDB plumbing (open, key encoding, durability, errors) so the three stores don't duplicate logic.

### Non-Goals
- Snapshot storage (`snapshotstore`) in v1 — see §11 (Open questions) for why it is deferred.
- Distributed / multi-writer coordination for a single actor. Each persistenceID is written by one process at a time (same model as the SQLite+Litestream proposals).
- Building, pinning, or vendoring the SlateDB Rust shared library. The module consumes the published Go module and documents the `LD_LIBRARY_PATH` requirement.
- Reimplementing SlateDB features (compaction, bloom filters, merge operators, checkpoints, GC). We use the binding's API rather than adding our own.

## 3. Background

### 3.1 eGo interfaces

**Durable state** (`persistence.StateStore`, mirroring `durablestore/postgres`):
```go
type StateStore interface {
    Connect(ctx context.Context) error
    Disconnect(ctx context.Context) error
    Ping(ctx context.Context) error
    WriteState(ctx context.Context, state *egopb.DurableState) error
    GetLatestState(ctx context.Context, persistenceID string) (*egopb.DurableState, error)
}
```
`WriteState` is an upsert keyed on `persistence_id` with a monotonically increasing `version_number`; `GetLatestState` returns the latest state or `nil` when absent.

**Event store** (`persistence.EventsStore`, mirroring `eventstore/postgres`):
```go
type EventsStore interface {
    Connect(ctx context.Context) error
    Disconnect(ctx context.Context) error
    Ping(ctx context.Context) error
    WriteEvents(ctx context.Context, events []*egopb.Event) error
    DeleteEvents(ctx context.Context, persistenceID string, toSequenceNumber uint64) error
    ReplayEvents(ctx context.Context, persistenceID string, fromSequenceNumber, toSequenceNumber uint64, limit uint64) ([]*egopb.Event, error)
    GetLatestEvent(ctx context.Context, persistenceID string) (*egopb.Event, error)
    PersistenceIDs(ctx context.Context, pageSize uint64, pageToken string) ([]string, string, error)
    ShardNumbers(ctx context.Context) ([]uint64, error)
    GetShardEvents(ctx context.Context, shardNumber uint64, offset int64, limit uint64) ([]*egopb.Event, int64, error)
}
```

**Offset store** (`persistence.OffsetStore`, mirroring `offsetstore/postgres`):
```go
type OffsetStore interface {
    Connect(ctx context.Context) error
    Disconnect(ctx context.Context) error
    Ping(ctx context.Context) error
    PutOffset(ctx context.Context, projectionID string, value uint64) error
    GetOffset(ctx context.Context, projectionID string) (uint64, error)
}
```

### 3.2 SlateDB and the Go binding

SlateDB is a cloud-native embedded key-value store: a log-structured-merge (LSM) database whose **write-ahead log, manifest, and sorted-string-table (SST) files are all stored in an object store**. It is the storage engine behind SlateDB's durable caching and is designed for latency-tolerant, cost-sensitive workloads. Key properties:

- **Everything lives in object storage.** Four logical namespaces in the store: `manifest/`, `wal/`, `compacted/`, `compactions/`. There is no local database file to back up — the object store *is* the database. A separate, lower-latency WAL object store can be set with `WithWalObjectStore`.
- **Durability is explicit.** A `Put` returns after updating the in-memory WAL + memtable; the data is durable only once flushed to object storage. The Go binding exposes this via `WriteHandle.AwaitDurable()` and `Db.Flush()`/`FlushWithOptions(FlushTypeWal|FlushTypeMemTable)`.
- **Atomic batches.** `Db.Write(batch)` applies a `WriteBatch` atomically and consumes it.
- **Range scans & iterators.** `Db.Scan(KeyRange)`, `ScanPrefix`, `DbIterator.Next()` / `NextBatch(max)` / `Seek(key)`.
- **Point-in-time snapshots.** `Db.Snapshot()` returns a consistent read-only view.
- **Admin/checkpoints/manifests.** `NewAdminBuilder(...)`, `CreateDetachedCheckpoint`, `ListManifests`, `ReadManifest`, `RunGcOnce` — useful for backups and restore, and a natural v2 path (see §11).

#### Object store construction (critical for the local/embedded mode)
The Go binding has **no** S3/GCS constructor functions; all backends are reached through a URL resolver:
```go
func ObjectStoreResolve(url string) (*ObjectStore, error) // e.g. "memory:///", "s3://bucket/path"
func ObjectStoreFromEnv(envFile *string) (*ObjectStore, error) // env-driven (cloud credentials)
func (self *ObjectStore) Destroy()
```
`ObjectStoreResolve("memory:///")` yields an in-process, in-memory object store with **zero AWS/network dependency** — this is what makes a fully-embedded SlateDB mode possible. `ObjectStoreFromEnv` wires S3/GCS/Azure/MinIO from environment variables for durable deployments.

#### Db construction
```go
func NewDbBuilder(path string, objectStore *ObjectStore) *DbBuilder
func (self *DbBuilder) WithSettings(settings *Settings) error
func (self *DbBuilder) WithWalObjectStore(walObjectStore *ObjectStore) error
func (self *DbBuilder) WithSeed(seed uint64) error
func (self *DbBuilder) WithSstBlockSize(size SstBlockSize) error
func (self *DbBuilder) Build() (*Db, error)
func (self *Db) Put(key, value []byte) (*WriteHandle, error)
func (self *Db) Get(key []byte) (*[]byte, error)          // nil = absent
func (self *Db) Delete(key []byte) (*WriteHandle, error)
func (self *Db) Scan(varRange KeyRange) (*DbIterator, error)
func (self *Db) Write(batch *WriteBatch) (*WriteHandle, error) // atomic
func (self *Db) Flush() error
func (self *Db) Shutdown() error
```
Keys and values are plain `[]byte` across the FFI. Key limit is 65,535 B (`u16::MAX`); value limit is 4 GiB (`u32::MAX`).

#### Settings
```go
func SettingsDefault() *Settings
func (self *Settings) Set(key, valueJson string) error // dotted path + JSON literal, e.g. Set("flush_interval", "\"250ms\"")
```
There is no typed `NewSettings(...)`; tuning is done via `SettingsDefault()` + `Set("dotted.path", jsonValue)`. Relevant keys for us: `flush_interval`, `compactor_options.*`, `object_store_cache_options.*`, `default_ttl_millis`.

#### Durability and close
- `WriteHandle.AwaitDurable() error` — blocks until that write is flushed to object storage (the per-write durability hook).
- `CloseOptions{ FlushType *FlushType }` with `FlushTypeWal` / `FlushTypeMemTable`; `ShutdownWithOptions` without a flush type means **no final flush (in-flight writes may be lost)** — our `Disconnect` must pass `FlushTypeWal`.

### 3.3 Why SlateDB for eGo
- **One store, all three interfaces** — durable state, events, and offsets share the same embedded engine and object-store backend.
- **No replication machinery to build.** Unlike SQLite+Litestream, durability is intrinsic: point the DB at S3 and the WAL/manifest/SSTs are durable by construction. Restore is "open the same object store and path."
- **Naturally append/range-friendly.** LSM range scans (`Scan`, `ScanPrefix`) map directly onto `ReplayEvents` and shard polling.
- **Zero-ops local mode** via `memory:///` for edge/dev, with a drop-in upgrade path to S3/GCS/Azure/MinIO for production — both from the same `Config`.

## 4. Design Overview

```
                        eGo actor / projection
                                  │
        ┌─────────────────────────┴─────────────────────────┐
        ▼                                                     ▼
┌───────────────────┐   ┌───────────────────┐   ┌───────────────────┐
│  StateStore       │   │  EventsStore      │   │  OffsetStore      │
│  durablestore/    │   │  eventstore/      │   │  offsetstore/     │
│  slatedb          │   │  slatedb          │   │  slatedb          │
└─────────┬─────────┘   └─────────┬─────────┘   └─────────┬─────────┘
          └───────────┬───────────┴───────────┬───────────┘
                      ▼                       ▼
          ┌─────────────────────┐   ┌──────────────────────┐
          │  shared internal    │   │  1 SlateDB Db per     │
          │  "slatedbkit"       │──▶│  persistenceID/       │
          │  (open, key enc,    │   │  projectionID (path)  │
          │   durability, err)  │   └──────────┬───────────┘
          └─────────────────────┘              │
                                               ▼
                    ┌──────────────────────────────────────────┐
                    │  ObjectStore (one per store, shared cfg)  │
                    │  "memory:///"  OR  s3:// / gs:// / az://  │
                    └──────────────────────────────────────────┘
```

### 4.1 Naming / mapping persistenceID → SlateDB keys
`persistenceID` / `projectionID` may contain characters that are awkward in object-store keys. We encode them deterministically with a reversible `keyID(persistenceID)` (base64url, no padding). Two conventions:

- **Single-DB-per-store model (primary):** one SlateDB `Db` per store instance, with every actor keyed under a `keyID(pid)` prefix. This keeps object count low and matches how SlateDB is intended to run (one store, many keys), and keeps cross-actor queries simple.
- **Per-actor-DB model (alternative):** a `Db` per persistenceID under a shared object-store path, mirroring the SQLite+Litestream "one instance per actor" approach. More isolation, but N `Db` handles and object prefixes. **Primary recommendation is the single-DB model** because SlateDB's multi-key range scans make cross-actor queries natural; see §9 for the trade-off.

This proposal's implementation sketch and cross-actor design assume the **single-DB-per-store model**.

### 4.2 Key encoding per interface

**Durable state** (one key per actor):
```
state/<keyID(pid)>                       → Value = DurableState envelope
```
`Value` is a single protobuf envelope containing the same fields as the Postgres row: `version_number`, `state_payload` (`proto.Marshal`), `state_manifest`, `timestamp`, `shard_number`. `WriteState` is a `Put` (upsert by key) — no version conflict logic needed for v1 (see §11 monotonicity guard). `GetLatestState` is a `Get`.

**Event store** (a range of keys per actor):
```
event/<keyID(pid)>/seq/<seq:8big>       → Value = Event envelope
```
The sequence number is encoded as an 8-byte big-endian integer so that a lexicographic `ScanPrefix("event/<keyID(pid)>/seq/")` returns events in sequence order — this is exactly `ReplayEvents`. Each `Value` is a protobuf envelope with `event_payload`, `event_manifest`, `timestamp`, `shard_number`, `is_deleted`, `encryption_key_id`, `is_encrypted` (mirroring the Postgres row).

- `WriteEvents`: build a `WriteBatch` of `Put`s (one per event) and `Db.Write(batch)` — **atomic**, matching the Postgres single-transaction insert.
- `DeleteEvents(pid, toSeq)`: `ScanPrefix` the actor's keys, `Delete` each key with `seq <= toSeq`, in a `WriteBatch` (atomic).
- `ReplayEvents`: `ScanPrefix("event/<keyID(pid)>/seq/")`, filter `seq BETWEEN from AND to`, apply `limit`, unmarshal. Because keys are sorted by big-endian seq, no sort is needed.
- `GetLatestEvent`: `ScanPrefix` and take the last key (or use `Db.Scan` on the reversed direction via a `KeyRange` with the seq upper bound) — returns `nil` when the prefix is empty.
- `PersistenceIDs`: `ScanPrefix("event/")`, collect distinct `keyID(pid)` from the key prefixes, keyset-paginate with `pageToken`.
- `ShardNumbers`: `ScanPrefix("event/")`, collect distinct `shard_number` from the decoded values (cache invalidated on write/delete, like the SQLite proposal).
- `GetShardEvents`: `ScanPrefix("event/")`, filter values by `shard_number == shard` and `timestamp > offset`, sort by timestamp, apply `limit`, return the last timestamp as the next offset. SlateDB keys are ordered by pid then seq, so this is a scan + filter — acceptable at the small/edge footprint this module targets.

**Offset store** (one key per projection):
```
offset/<keyID(projectionID)>             → Value = 8-byte big-endian uint64
```
`PutOffset` = `Put`; `GetOffset` = `Get`, return `0` when absent (matching Postgres semantics).

### 4.3 Durability mode (the central design knob)
Because SlateDB writes are durable only after a flush to object storage, each store exposes a `Durability` setting:

- **`DurabilityNone`** — return after the in-memory `Put`/`Write` completes. Fastest; data is lost on crash before a flush. Only for the `memory:///` embedded mode or where RPO=∞ is acceptable.
- **`DurabilityDurable` (default)** — call `WriteHandle.AwaitDurable()` on the write handle returned by `Put`/`Write` before returning. Smallest RPO: the write is flushed to the object store before `WriteState`/`WriteEvents`/`PutOffset` returns. Cost is an object-store flush per write/batch.
- **`DurabilityFlush` (optional)** — return after the local commit, then trigger `FlushWithOptions(FlushTypeWal)` on a configurable interval (background goroutine), with a final synchronous flush on `Disconnect`. Higher throughput, RPO bounded by the interval.

In `DurabilityDurable`, a `WriteEvents` batch amortizes one flush across many events — cheap relative to the per-write durable-state case, exactly as in the SQLite+Litestream proposal.

### 4.4 Object store per interface
Each store builds its own `ObjectStore` from the shared `Config`, under a distinct root path so durable state, events, and offsets never collide:

```
s3://my-bucket/ego/durable/<...>
s3://my-bucket/ego/events/<...>
s3://my-bucket/ego/offsets/<...>
```
The root path (`BucketURL`) is required in cloud mode; in embedded mode it is ignored and `memory:///` is used.

## 5. Concurrency & Consistency

- **Single-writer per actor:** eGo actors write their own persistence sequentially, so one `Db` with SlateDB's internal serialization is sufficient. The `Db` handle is safe for concurrent use per the binding; we additionally guard `Connect`/`Disconnect` with a mutex and track a `connected` flag like sibling stores.
- **Atomic batches:** `WriteEvents` uses `Db.Write(batch)` so a journal batch is all-or-nothing. `DeleteEvents` is also a single batch.
- **Read-your-writes:** SlateDB reads include the in-memory WAL + memtable by default, so `WriteState` then `GetLatestState` within the same process observes the write even before a flush. `DurabilityDurable` additionally guarantees cross-process visibility.
- **No cross-actor lock contention:** unlike a shared SQL database, there are no table/row locks; SlateDB handles concurrency internally.
- **Monotonicity guard (optional):** for durable state, optionally reject `WriteState` whose `version_number` is not strictly greater than the stored one (read-then-write under the store's serialization) to fail fast on out-of-order writers. Not required for v1 correctness given eGo's sequencing.

## 6. Lifecycle & the `Db`/`ObjectStore` seam

Each store wraps a single SlateDB `Db` plus its `ObjectStore`:

```go
type store struct {
    cfg       *Config
    db        *slatedb.Db
    os        *slatedb.ObjectStore
    mu        sync.Mutex
    connected bool
}
```

- `Connect(ctx)`: validate config; build `ObjectStore` (`memory:///` or cloud URL); `NewDbBuilder(rootPath, os)`, apply `WithSettings`/`WithSeed`/`WithWalObjectStore` from config; `Build()`; mark connected. Idempotent.
- `Ping(ctx)`: ensure connected; optionally `Status()` (exposed as `Db.Status() DbStatus`) or a lightweight `Get` on a sentinel key.
- `Disconnect(ctx)`: if `DurabilityFlush`, stop the background flusher and do a final `FlushWithOptions(FlushTypeWal)`; `Db.ShutdownWithOptions(CloseOptions{FlushType: &FlushTypeWal})`; `ObjectStore.Destroy()`; clear connected.
- The `Db` handle is created once and shared; SlateDB manages the LSM compaction/flush lifecycle internally. A future LRU of per-actor `Db`s belongs to the per-actor-DB model (§4.1 / §9), not the primary design.

## 7. Config

```go
type Config struct {
    // Object store
    ObjectStoreURL string        // e.g. "s3://my-bucket/ego/durable", "memory:///" (embedded), "gs://...", "az://..."
    EnvFile        *string       // optional; when set, uses ObjectStoreFromEnv(envFile)
    Region         string        // optional; S3 region override
    Endpoint       string        // optional; S3-compatible endpoint (MinIO/LocalStack)
    UsePathStyle   bool          // required for MinIO/LocalStack

    // SlateDB
    Settings       map[string]string // dotted path -> JSON literal, applied via Settings.Set
    WalObjectStore *string       // optional separate WAL object store URL (WithWalObjectStore)
    Seed           *uint64       // optional seed

    // eGo behavior
    Durability     Durability   // None | Durable (default) | Flush
    FlushInterval  time.Duration // interval for DurabilityFlush background flusher (default 1s)
}
```

`NewDurableStore(cfg)`, `NewEventsStore(cfg)`, `NewOffsetStore(cfg)` each return the respective store. A shared `NewConfig` helper (in `slatedbkit`) normalizes defaults so the three modules behave identically.

## 8. Module layout

```
durablestore/slatedb/
  durable_store.go    # StateStore implementation
  config.go           # Config + defaults (may delegate to shared kit)
  Earthfile           # module build (mirrors sibling modules)
  go.mod              # module go.mod (requires CGO + slatedb shared lib)
  README.md

eventstore/slatedb/
  events_store.go     # EventsStore implementation (incl. cross-actor queries)
  config.go
  Earthfile
  go.mod
  README.md

offsetstore/slatedb/
  offset_store.go     # OffsetStore implementation
  config.go
  Earthfile
  go.mod
  README.md

internal/slatedbkit/  # shared, unexported plumbing (same module or a shared internal pkg)
  objectstore.go      # ObjectStore resolution (memory/cloud) + URL helpers
  keyencode.go        # keyID(), seq big-endian encode/decode
  envelope.go         # protobuf envelopes <-> egopb.DurableState / egopb.Event
  durability.go       # Durability mode + AwaitDurable / flush loop
  config.go           # shared Config + defaults
```

Ships as `github.com/tochemey/ego-contrib/durablestore/slatedb`, `.../eventstore/slatedb`, `.../offsetstore/slatedb`.

### Build / deployment note
The `slatedb-go/uniffi` module requires **Go 1.25+, `CGO_ENABLED=1`, a C toolchain, and the `slatedb_uniffi` shared library** available on the loader path (`LD_LIBRARY_PATH`/`DYLD_LIBRARY_PATH`). The Earthfile and each README must document this; the integration tests must set the loader path. This is the single biggest operational difference from the pure-Go modules and drives Open Question #2.

## 9. Trade-offs: single-DB-per-store vs per-actor-DB

### Single-DB-per-store (primary)
**Advantages**
- One `Db` + one object-store prefix per store → low object count, one handle, simple lifecycle.
- Cross-actor queries (`PersistenceIDs`, `ShardNumbers`, `GetShardEvents`) are prefix scans over one store — natural for SlateDB's LSM.
- Mirrors SlateDB's intended usage (one store, many keys).

**Disadvantages / risks**
- All actors share one store; a misbehaving compaction/write pattern in one actor can affect the whole store (mitigated by SlateDB's built-in isolation of SSTs and compaction).
- Restore/GC is store-wide rather than per-actor (fine at this footprint; `Admin.RunGcOnce` helps).

### Per-actor-DB (alternative, matches SQLite+Litestream)
**Advantages**
- Per-actor isolation of compactions, restore, deletion; a corrupted/over-large actor doesn't affect others.

**Disadvantages / risks**
- N `Db` handles and N object prefixes; cross-actor queries become fan-out (the exact problem the SQLite event proposal wrestles with), and SlateDB was not designed for thousands of open `Db` handles per process.

The single-DB model is recommended; the per-actor model is a follow-up if isolation becomes a requirement.

## 10. Testing strategy

- **Unit tests (embedded `memory:///` mode, no network/container):**
  - Durable state: upsert semantics, `GetLatestState` returns `nil` on empty, per-actor isolation, manifest round-trip.
  - Events: `WriteEvents` atomicity (a batch with a duplicate seq is rejected / all-or-nothing), `DeleteEvents` inclusive boundary, `ReplayEvents` ordering + range + limit, `GetLatestEvent` on empty returns `nil`, `PersistenceIDs` pagination, `ShardNumbers` union, `GetShardEvents` merge/sort/limit with multiple actors in one shard.
  - Offsets: `PutOffset`/`GetOffset` round-trip, `GetOffset` returns 0 when absent.
  - Durability modes: assert `AwaitDurable` path is exercised in `DurabilityDurable`.
- **Integration tests (Testcontainers-Go + MinIO, matching sibling modules):**
  - Object-store mode against MinIO (`s3://bucket/...` with endpoint/path-style config): writes land in the bucket; a fresh store opening the same path recovers state/events/offsets (restore-by-reopen).
  - Restart/recovery: write with `DurabilityDurable`, "lose" the process, reopen, verify full data is present.
  - `DurabilityNone` vs `DurabilityDurable` behavior.
- **Earthfile:** `+test` target mirrors sibling modules (`WITH DOCKER` running MinIO, `go test -race`), with the `slatedb_uniffi` loader path exported.

## 11. Open questions

1. **`Durability` default.** `DurabilityDurable` (per-write flush to the object store, smallest RPO) vs `DurabilityNone`/`DurabilityFlush` (higher throughput, wider RPO). Validate per-write flush latency is acceptable on the eGo write path before committing; consider defaulting to `Durable` for state and `Flush` for high-volume events.
2. **CGO + shared-library dependency.** `slatedb-go/uniffi` is cgo + a Rust shared lib (Go 1.25+, `CGO_ENABLED=1`, loader path). This is a heavier operational footprint than the pure-Go modules and conflicts with the repo's pure-Go simplicity. Confirm this is acceptable, or whether a pinned/vendored Rust binary or a prebuilt wrapper should be considered.
3. **Single-DB vs per-actor-DB.** Confirm the single-DB-per-store model (§4.1/§9) is preferred over matching the SQLite+Litestream per-actor layout.
4. **`DeleteEvents` semantics.** Physical delete of keys (Postgres parity) vs tombstone (`is_deleted`) to preserve replay/audit history. SlateDB tombstones are cheap (compaction reclaims space) — a tombstone path may be preferable for auditability.
5. **Snapshot store.** Deferred from v1 because `snapshotstore` has a distinct eGo interface and value type; SlateDB checkpoints (`Admin.CreateDetachedCheckpoint`) are a natural v2 mechanism. Confirm scope.
6. **Monotonicity guard for durable state.** Read-then-write to reject out-of-order `version_number`, or trust eGo to sequence writes.
7. **`memory:///` durability expectations.** In embedded mode, `DurabilityDurable` flushes to an in-process store that is still lost on process death. Ensure README sets expectations that only cloud-mode provides cross-process durability.
8. **Reuse between stores.** Whether the three modules share `internal/slatedbkit` within a single Go module or ship as three separate modules each importing a shared internal package (the repo currently uses one module per store — see sibling SQLite proposals' shared-plumbing open question).
9. **Shared library distribution.** How to distribute the `slatedb_uniffi` shared library for the Earthfile builds and for downstream users (build-tag, container image, or documented install step).

## 12. References

- SlateDB Go binding docs: https://pkg.go.dev/slatedb.io/slatedb-go/uniffi (v0.16.0)
- SlateDB module: https://pkg.go.dev/slatedb.io/slatedb-go
- SlateDB site: https://slatedb.io (quickstart, configuration, FAQ; object-store backends: S3, GCS, ABS, MinIO/Tigris, in-memory; WAL-in-object-storage durability semantics)
- eGo interfaces: `github.com/tochemey/ego/v4/persistence` → `StateStore`, `EventsStore`, `OffsetStore` (as implemented by `durablestore/postgres`, `eventstore/postgres`, `offsetstore/postgres` in this repo)
- Sibling design proposals (shared conventions): `docs/design/sqlite-litestream-durable-state.md`, `docs/design/sqlite-litestream-event-sourcing.md`
