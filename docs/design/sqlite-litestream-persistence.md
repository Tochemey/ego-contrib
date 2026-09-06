# Design: SQLite + Litestream Persistence for eGo (one instance per actor)

- **Status:** Proposal (draft for review)
- **Modules:** `durablestore/sqlite` (durable state), `eventstore/sqlite` (event-sourced journal)
- **Scope:** Durable state (`persistence.StateStore`) and event-sourced persistence (`persistence.EventsStore`). Projection offset (`offsetstore`) and snapshot (`snapshotstore`) storage are out of scope.
- **Inspired by:** celld's production SQLite-in-S3 architecture — see `docs/design/sqlite-s3-celld-comparison.md`.
- **Maintainer:** eGo Contrib community

## 1. Motivation

eGo persistence currently requires an external database (PostgreSQL, DynamoDB, Cassandra) or is held only in memory (tests/PoC). For small, single-node deployments — edge devices, embedded runtimes, development, or workloads with modest state per actor — a full external database is overkill:

- provisioning and operational burden
- network dependency / latency
- no easy "state lives with the process" story

SQLite gives us a zero-ops, embedded, transactional datastore on the local filesystem. By streaming its write-ahead log (WAL) to S3 with the embedded [Litestream](https://litestream.io) library, we recover the durability of a remote store without running a database server: local reads/writes stay local and fast, while the WAL is continuously replicated to object storage for disaster recovery and restart-from-backup.

The core design decision is **one SQLite database (and therefore one Litestream replica stream) per actor** rather than a single shared database for all actors. celld runs thousands of such per-cell databases per node in production, validating the model.

## 2. Goals / Non-Goals

### Goals
- Implement the eGo `persistence.StateStore` and `persistence.EventsStore` interfaces.
- Store durable state / append-only event journals in local SQLite, 1 actor = 1 SQLite file.
- Replicate each actor's WAL to S3 via the embedded Litestream library so state survives node loss and can be restored.
- Restore a single actor's state/journal from S3 on demand.
- Amortize S3 replication cost across actors with group commit (bundle log tier + fold).
- Support the event store's cross-actor queries (`PersistenceIDs`, `ShardNumbers`, `GetShardEvents`) at the target footprint.

### Non-Goals
- Projection offset persistence (`offsetstore`) and snapshot storage (`snapshotstore`).
- Multi-writer / distributed coordination across nodes for a single actor. This store is intended for a local actor running in one process at a time (epoch fencing, Section 9.7, is the mitigation for the single-writer assumption).
- Global indexes / analytic queries over all events beyond the interface's required queries.
- Sharding/partitioning logic beyond the per-actor file split.

## 3. Background

### 3.1 eGo persistence interfaces

**Durable state** (mirroring `durablestore/postgres`):

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

**Event sourcing** (mirroring `eventstore/postgres`):

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
- `PersistenceIDs`, `ShardNumbers`, `GetShardEvents` span actors/shard memberships — the design challenge (Section 6).

### 3.2 SQLite WAL mode

SQLite's write-ahead log: all writes go to a `-wal` file, later checkpointed back into the main database file. This is exactly the artifact Litestream replicates to S3. Config:
- `PRAGMA journal_mode = WAL`
- `PRAGMA synchronous = NORMAL` (not FULL — replicated-WAL convention; durability comes from replication, not local fsync)
- `PRAGMA busy_timeout`

### 3.3 Litestream (embedded library)

Litestream ships a CLI daemon and — the approach taken here — an **embedded Go library** (`github.com/benbjohnson/litestream`) linked directly into the module. No daemon, no generated config file, no embedded binary.

Replication is two-stage (verified against the upstream source):

- **Capture — `DB.Sync(ctx)`:** reads the SQLite WAL, verifies continuity, writes **L0 LTX files locally** to `.<db>-litestream/ltx/0/<min>-<max>.ltx`. Purely local, no network.
- **Upload — `Replica.Sync(ctx)`:** lists the replica's remote position, then uploads each not-yet-uploaded local L0 file to S3 (one PUT per L0) at litestream's exact keys. Never touches SQLite or the WAL.
- **`DB.SyncAndWait(ctx)`:** both, blocking.
- **`Replica.Restore(...)`:** the inverse — downloads a contiguous snapshot + LTX chain and applies it into a local database file. The exact call is version-specific; the implementation must pin the Litestream version and adapt its `RestoreOptions` API.
- **`Replica.Start` / `DB.Open`** auto-start background monitor goroutines; `Replica.MonitorEnabled=false` disables the auto-uploader for manual/synchronous use (required by group commit, Section 8).

Each `DB` (SQLite file) maps one-to-one to a `Replica` (S3 prefix).

## 4. Design Overview

```
Actor (persistenceID = "account-42")
        │
        ▼
┌─────────────────────────┐     WAL      ┌──────────────────────────┐
│  StateStore / EventsStore│─────────────▶│  SQLite file per actor    │
│  (sqlite)               │  local r/w   │  data/actors/account-42   │
└─────────────────────────┘              │   account-42              │
                                         │   account-42-wal           │
                                         └────────────┬─────────────┘
                                                      │  litestream DB.Sync(ctx)
                                                      │  (embedded, per actor)
                                                      ▼
                                         ┌──────────────────────────┐
                                         │  S3 bucket               │
                                         │  s3://ego-state/actors/  │  (or ego-journal)
                                         │    account-42/           │
                                         └──────────────────────────┘
```

### 4.1 File layout

One directory holds all per-actor databases:

```
dataDir/
  <persistenceID>/
    <persistenceID>.db       # SQLite database
    <persistenceID>-wal      # WAL file (streamed to S3 by litestream)
    <persistenceID>-shm      # shared-memory index (WAL mode)
```

The per-actor **directory** (rather than a flat `<pid>.db` file) keeps each actor's replica self-contained and gives Litestream a clean S3 prefix per actor.

### 4.2 Naming / mapping

`persistenceID` may contain characters unsafe for filesystem/S3 keys. A deterministic escaping function (`fileKey(persistenceID)`) normalizes the name (e.g. base64url-encoded or hex of the SHA-256) while preserving a stable, reversible mapping. No reverse index is needed: restore is keyed by persistenceID directly, and the event store's `PersistenceIDs` is a directory listing of `dataDir`.

### 4.3 Connection model

Each SQLite database is opened lazily on first access for that `persistenceID` and cached in an in-memory map (`map[persistenceID]*actorDB`), capped by an LRU (`MaxOpenDBs`) that closes idle handles.

- WAL mode allows a single writer plus concurrent readers; `SetMaxOpenConns(1)` serializes writes per actor, matching eGo's per-actor sequential updates.
- `busy_timeout` prevents immediate `SQLITE_BUSY` on contention.

### 4.4 Schema (per-actor tables)

**Durable state** — single-row-per-actor, mirroring the Postgres schema:

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

**Event journal** — append-only, mirroring the Postgres schema:

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

Since each DB holds a single actor, the `(persistence_id, sequence_number)` PK degrades to a `sequence_number` uniqueness constraint — the append-only guarantee: **no duplicate sequence numbers** per actor. Payloads are serialized protobuf (`proto.Marshal`) with the full message name as the manifest, matching the postgres stores' `row.go` mapping.

## 5. Durable State Store (durablestore/sqlite)

### 5.1 WriteState

- Open a single transaction, `INSERT ... ON CONFLICT(persistence_id) DO UPDATE` (idempotent upsert), mirroring the Postgres store.
- Then synchronize per `SyncMode` (Section 7.3 / 8).

### 5.2 GetLatestState

- `SELECT ... WHERE persistence_id = ?` on that actor's DB; `nil` when absent.

## 6. Event-Sourced Persistence (eventstore/sqlite)

### 6.1 WriteEvents

- An empty event list is a no-op.
- All events in one call must have the same `persistence_id`. The store rejects mixed-ID batches before opening a transaction because one actor maps to one SQLite file and SQLite cannot atomically commit across multiple actor files.
- Single SQLite transaction (`BEGIN IMMEDIATE`), insert all events; any duplicate `sequence_number` violates the PK and aborts — surfacing a conflicting-sequence error to eGo.
- No batching-by-parameter-limit needed: SQLite's variable limit (default 32766 in modern versions) is far above typical eGo batches; chunk only if a batch exceeds it.
- Commit, then synchronize per `SyncMode`.

### 6.2 DeleteEvents

- Physical `DELETE FROM events_store WHERE sequence_number <= ?` on that actor's DB, in a transaction.
- Deletes produce WAL traffic just like inserts. Note: deleted data is not reclaimed in S3 immediately; retention (Section 7.3) is what eventually reclaims space.

### 6.3 ReplayEvents / GetLatestEvent

- Per-actor `SELECT ... WHERE sequence_number BETWEEN ? AND ? ORDER BY sequence_number ASC LIMIT ?`; rows unmarshalled with the shared `toProto` manifest pattern.
- `GetLatestEvent` = same query `ORDER BY sequence_number DESC LIMIT 1`, `nil` when absent.

### 6.4 Cross-actor queries (the per-actor model's special case)

**PersistenceIDs** — directory listing, not SQL: enumerate subdirectories of `dataDir`, apply `fileKey` decoding, sort, paginate with `pageToken` as a keyset (`> pageToken`). Zero DB reads.

**ShardNumbers** — fan-out with caching: for each actor DB run `SELECT DISTINCT shard_number FROM events_store`, union the results. Cache the result (invalidated on any write/delete/restore) to avoid rescanning every call.

**GetShardEvents** — fan-out + merge: per actor DB run `SELECT ... WHERE shard_number = ? AND timestamp > ? ORDER BY timestamp ASC LIMIT ?`; merge per-actor results, sort by timestamp, apply the global `limit`, return the last timestamp as the next offset. Cost is O(#actors × per-actor scan); acceptable for small/edge footprints (tens to low-hundreds of actors per process). At larger scale this is the first thing to revisit (Open Questions #4).

## 7. Litestream integration (embedded library)

### 7.1 In-process replication

Each actor DB is wrapped in a `*litestream.DB` with exactly one `*litestream.Replica` pointing at that actor's S3 prefix (one SQLite instance per actor = one replica stream):

```
actorDB (wraps *sql.DB + *litestream.DB)
   │  WriteState/WriteEvents → INSERT/upsert
   │  db.Sync(ctx)  (or group-commit capture)  ──▶ s3://ego-state/actors/<pid>/
   ▼
   litestream.Replica (retention, snapshot interval)
```

### 7.2 Per-actor replica construction

```go
lsDB := litestream.NewDB(dbPath)
replica := litestream.NewReplica(lsDB)
replica.Client = newS3ReplicaClient(cfg, actorID)
replica.MonitorEnabled = false // group committer owns capture/upload scheduling
lsDB.Replica = replica
```

- The replica is constructed with `NewReplica(lsDB)` and receives a configured `ReplicaClient`; the exact client factory is implementation-specific and must be pinned to the selected Litestream version.
- Credentials resolve through the standard AWS credential chain (env, shared config, IAM role).
- Opening the underlying SQLite in WAL mode is required before Litestream can replicate; the module enforces `PRAGMA journal_mode = WAL`.

### 7.3 Sync semantics

Durability is achieved by synchronizing the WAL to S3. Three modes, exposed on `Config`:

- **SyncOnWrite (legacy):** `WriteState`/`WriteEvents` performs the SQL write, then `DB.SyncAndWait(ctx)` before returning. `DB.Sync` captures the WAL locally and `Replica.Sync` uploads the resulting L0 files. Smallest RPO (a committed write is already in S3); cost is one S3 round-trip per write. For event batches this is cheap (one round-trip amortizes many events).
- **SyncInterval:** a background goroutine periodically runs the capture and upload stages (`DB.Sync` followed by `Replica.Sync`); writes return after the local commit. Higher throughput, RPO bounded by `SyncInterval`; `Disconnect` performs a final synchronous sync. **Note:** this still uploads one object per actor per tick — no cross-actor sharing.
- **GroupCommit (recommended default):** barrier group commit with a bundle log tier — the ack-gated design of Section 8. RPO=0 with ~1 S3 PUT per flush across all actors.

Because eGo actor state updates are naturally serialized per actor, syncs for different actors happen independently and never contend.

### 7.4 Restore flow

Restore uses the replica's `Restore` method against the actor's S3 prefix:

```
if !exists(localDB) && replicaExistsInS3:
    replica.Restore(ctx, litestream.RestoreOptions{
        OutputPath: dbPath,
    })  // pull snapshot + generations into local file
```

- Triggered lazily inside `actorDB(ctx, pid)` on first access when the local DB file is absent (full design in Section 9.3, restore path). A bundle tail is not readable by stock Litestream restore; it must first be materialized by the bundle-aware fold described in Section 9.3.
- If no replica exists in S3, the actor starts fresh (normal first-run).
- A synchronous restore blocks access for that actor only; other actors' DBs are unaffected.

## 8. Group commit: amortizing S3 replication across actors

### 8.1 Problem

With one SQLite file per actor, naive replication is one S3 upload per actor write. A node with `N` actors writing at `W` writes/s does `N·W` S3 PUTs/s; S3 bills per request and small segments pay full request cost. Group commit fixes this: **many writes, across many actors, share one upload and one durability ack.**

### 8.2 Design goals

1. Cut the S3 PUT rate by 1–2 orders of magnitude under write fan-out.
2. Keep a defined RPO: RPO=0 (ack-gated, writes block until bucket-proof) or a bounded window (optimistic return + periodic barrier).
3. Preserve the per-actor object layout so restore stays granular and cheap.
4. Preserve epoch fencing: a fenced writer must not corrupt the group.

### 8.3 Option 1 — Barrier group commit (temporal, per-node single uploader)

A single **committer** owns all replication uploads. The write path never touches S3:

```
WriteState / WriteEvents
   │  (local SQLite commit — fast, no S3)
   ▼
commit log: per-actor pending queue (captured L0 segment, dirty flag)
   │
   ▼  every group_interval, or when bytes/actors exceed a threshold:
committer: capture WAL of every dirty actor → one or more L0 segments per actor
   │  upload with bounded parallelism (e.g. 8–16 slots)
   ▼
bucket: per-actor prefixes <pid>/<segment> (unchanged layout)
```

- **Synchronous variant (recommended):** each write blocks until the *covering* bundle upload is proven durable. Writes within one interval share one bundle flush and one ack; effective latency ≈ `group_interval + upload`. PostgreSQL/InnoDB-style barrier group commit; **RPO=0**.
- **Optimistic variant:** writes return after the local commit; a background committer proves durability within `group_interval`. Cheapest latency; RPO = `group_interval`; needs a barrier (`Disconnect`) for clean shutdown.
- **Coalescing boundary:** writes inside one SQLite transaction produce one L0 segment. Separate committed writes produce separate L0 segments; group commit batches their upload/proof rather than merging already-committed transactions.

**Trade-offs:** still one PUT per actor per flush — PUT count scales with active actors per interval, not with writes. The bundle tier (Option 2) removes the last per-actor PUT.

### 8.4 Option 2 — Bundle log tier (true cross-actor single PUT)

Folds every dirty actor's captured segment into **one bundle object per flush**:

```
bucket layout (addition):
  bundles/<node-id>/<seq>.bundle      ← the ack target, one PUT per interval
  actors/<pid>/...                    ← per-actor prefix, now a restore/tier target only

bundle payload:
  header: sequence, epoch, [ (actor, epoch, min_txid, max_txid, offset) ... ]
  body:   concatenated L0 segments, in (actor, txid) order
```

- The **durability proof is the bundle object**: a write's ticket names `(bundle_seq, offset)`. All actors in the flush are proven by the one PUT. The bundle is a custom durability log, not a Litestream replica object; it requires a bundle-aware materializer and takeover reader.
- PUT rate drops to ~1 per interval per node regardless of how many actors wrote.
- **Restore stays per-actor:** a successor reading actor A gathers A's segments — first from A's own prefix, then extracting A's tail from retained bundles with the bundle-aware materializer — before invoking Litestream restore. The per-actor prefix remains the Litestream-compatible restore truth; bundles are an accelerator until folded.
- **Retention/GC:** after an actor's segments are materialized into its prefix and compacted, bundles older than the newest materialized cut are deleted.

**Trade-offs:** restore of an actor whose newest tail lives only in bundles needs the gather step (one extra pass); followers must understand the bundle format.

### 8.5 Option 3 — Hybrid (recommended)

1. **Write path = barrier group commit.** Local commit, enqueue, wait for covering proof. RPO=0, one ack per flush, per-actor coalescing.
2. **Upload path = bundle tier with per-actor fold.** The committer writes one custom bundle per interval (the ack target). Bounded materializer workers extract each actor's L0 entries and write them through a Litestream-compatible replica client into the per-actor prefix **concurrently with serving**; compaction runs against the prefix.
3. **Fallback = per-actor prefix upload** when the bundle tier is disabled or a bundle PUT fails — same proof semantics, one extra upload slot.

```
                   ┌────────────────────────────────────────────┐
WriteState ───────▶│ committer (one per node)                    │
   (local commit)  │  every group_interval:                      │
                   │   · capture dirty actors' L0 segments       │
                   │   · write bundles/<node>/<seq>.bundle  ◀────│── ack target (1 PUT)
                   │   · notify waiters whose ticket ≤ seq       │
                   └────────────────────────┬───────────────────┘
                                            │ background materialize (bounded workers)
                                            ▼
                          fold → actors/<pid>/<segment>  →  compact L1/L9
                          (restore + tiering read the prefix)
```

**Why this shape:** the ack path never touches the per-actor prefixes (PUT cost ≈ 1/interval); normal restore reads the per-actor Litestream-compatible prefix. A takeover may first read bundles to materialize an un-folded tail, then uses the same granular restore path.

### 8.6 Correctness properties

- **Ordering:** segments within a bundle are ordered by `(actor, txid)`; a bundle has a monotone sequence; a write's ticket = `(bundle_seq, offset)`. A flush is atomic — it covers all listed actors or none.
- **Durability proof:** a proof for `bundle_seq` covers every write whose ticket `≤ (seq, offset)`. Proof is *idempotent* — a proof for a later bundle implies all earlier ones.
- **Epoch fencing:** bundles carry the owning epoch and live under `bundles/<node>/<epoch>/`; a fenced writer's bundles write a dead prefix. Materialization refuses a segment whose epoch is not the current owner's.
- **Retention boundary:** never delete a bundle still referenced by an un-materialized actor's newest cut; GC only after fold + compaction confirms the prefix is ahead.
- **Failure semantics (celld's lesson):** the wait distinguishes a *queue* (healthy node, uploads behind) from a *stall* (stuck upload). Extend waits while the node lands proofs; fail a stuck upload on a fixed budget — not a fixed deadline from each write's commit.
- **Empty flushes:** emit no bundle when nothing is dirty.

### 8.7 Knobs (Config additions)

```go
type GroupCommit struct {
    Interval           time.Duration // flush cadence; also the ack latency floor (default 10ms)
    MaxBatchBytes      uint64        // flush early when a batch exceeds this (default 4 MiB)
    MaxActors          int           // flush early when too many actors are dirty (default 256)
    UploadSlots        int           // parallel segment/bundle uploads (default 8–16)
    Bundle             bool          // enable the bundle tier (default true)
    MaterializeWorkers int           // concurrent fold workers (default 4)
    WaitBudget         time.Duration // proof deadline per flush (default 30s)
}
```

The `SyncMode` enum becomes: `SyncOnWrite` (legacy), `GroupCommit` (barrier, RPO=0), `AsyncInterval` (optimistic, RPO=interval).

### 8.8 eGo interface impact

- `WriteState` / `WriteEvents` in **GroupCommit** mode block until the covering proof lands → semantics equivalent to `SyncOnWrite`, but amortized. This is the safe default.
- `Disconnect` performs a final barrier flush (prove all pending tickets) before closing DBs — preserves durability across clean shutdown.
- `GetLatestState` / `ReplayEvents` are unaffected: reads are local. A read-only answer may optionally report how fresh it is (celld's `observed_position`), but that is additive.

### 8.9 Recommendation

Adopt **Option 3 (hybrid)** as the default in both modules, keeping `SyncOnWrite` for latency-critical single-actor tests. Order of work:

1. Barrier committer + per-actor coalescing (Option 1) — biggest win, least machinery.
2. Bundle tier (Option 2) as the ack target + fold — removes per-actor PUTs at scale.
3. Materialize-on-restore + bundle GC — closes the lifecycle.

This mirrors celld's proven production shape (log-tier bundles for the ack, per-cell prefixes for restore) while staying a straightforward extension of the embedded-Litestream design.

## 9. Folding the bundle into per-actor replicas

The bundle is the ack target; the per-actor S3 prefix is the restore/tiering target. **Folding** is moving each actor's captured segments from the bundle into that actor's prefix.

### 9.1 What "fold" means (and does not mean)

- **It is NOT applying SQL.** Each bundle entry is already the exact L0 LTX segment bytes that a litestream replica upload would have produced (the per-actor `DB` captured the WAL, encoded the segment, and handed the bytes to the committer). The local SQLite file already holds the committed data — nothing is replayed, no second SQLite is opened.
- **It IS writing objects.** For each actor, PUT the segment bytes to the actor's prefix at the standard Litestream LTX key, in txid order, plus whatever generation metadata the pinned `ReplicaClient` requires. After the fold, the actor's prefix is a Litestream-compatible replica, so the pinned `Restore` implementation can read it.

```
bundle bundles/<node>/<seq>.bundle            actors/<pid>/ltx/<min>-<max>.ltx (L0)
   ┌────────────┬────────────┐                 actor A: min_a-max_a, min_b-max_b, ...
   │ actor A    │ segment a  │  ── fold ──▶   actor B: ...
   │ actor B    │ segment b  │                (exactly the keys litestream derives:
   │ ...        │ ...        │                 ltx_file_path / level dirs / snapshots)
   └────────────┴────────────┘
   (ack target: 1 PUT/flush)                   (restore + compaction read here)
```

### 9.2 When folds happen

Folding is background and eventually consistent — it is **not** on the ack path. Three triggers:

1. **Periodic drain:** a materializer walks retained bundles in `seq` order and folds segments for actors whose folded-watermark lags.
2. **Eviction:** before a node evicts an actor, fold its tail so any successor can restore from the prefix alone.
3. **Takeover / restore:** a successor that must restore actor A folds A's tail out of retained bundles *before* reading the prefix (celld's `fold_cell` before `epoch_chain`). Hard requirement: an acked write must be in either the prefix or a retained bundle at restore time.

### 9.3 Strategy A — litestream's own replica sync is the same-node fold (recommended)

Instead of inventing a folder, reuse litestream's upload path:

- The per-actor `DB` keeps capturing L0s into its local meta dir (`.db-litestream/ltx/...`). The committer **reads those freshly-captured bytes** to build the bundle — the bundle holds exactly what the replica would upload.
- The ack waits on the bundle object (`bundle_seq`). On the same node, the per-actor `Replica.Sync(ctx)` — Litestream's own uploader — is invoked in the background by the materializer. It uploads the captured L0s to the actor's prefix at the keys Litestream derives.
- **Result:** zero risk of key-layout drift for same-node materialization. A takeover node cannot use `Replica.Sync` for the bundle tail because the local L0 files are absent; it must use the bundle-aware direct materializer described in the restore path and Strategy B.

Operational constraints:
- **Disable the auto-monitors** (`Replica.MonitorEnabled=false`, `MonitorInterval=0`): `DB.Open()` auto-starts a capture loop and `init()` calls `Replica.Start` (an upload loop). In group-commit mode these must be off so writes don't self-upload and defeat the bundle.
- **Fold before local L0 cleanup:** `Replica.Sync` opens the local file; if it is gone (compaction / `L0Retention`), that segment must come from the bundle bytes (Strategy B for that segment).
- **Skip the position LIST:** `Replica.Sync` does an S3 LIST (`calcPos`) per actor per sync unless the position is seeded. Track what you've uploaded and call `Replica.SetPos(...)` to skip it.

#### The three paths (Strategy A)

**Write path** — ack-gated, RPO=0, no S3 on the hot path:

```
WriteState(actor A)                WriteEvents(actor B)
      │                                  │
      ▼                                  ▼
local SQLite commit ──────────►   local SQLite commit        (no network)
      │                                  │
      ▼                                  ▼
DB.Sync(ctx)  == CAPTURE ─────►   DB.Sync(ctx)               (no network)
WAL → local L0 file                    │
.<A>-litestream/ltx/0/<t1>.ltx         ▼
      │                           .<B>-litestream/ltx/0/<t2>.ltx
      └───────────────┬────────────────┘
                      ▼   committer (every group_interval)
        bundle PUT:  bundles/<node>/<seq>.bundle              (1 S3 PUT)
        { header: seq, [(A,t1),(B,t2),...]
          body:   concat(L0_A, L0_B, ...) }
                      │
                      ▼
        ack: waiters with ticket ≤ seq wake
        → WriteState/WriteEvents return (durable)
```

S3 cost per write: zero. Per flush: one PUT shared by all dirty actors. Latency ≈ flush interval + one PUT.

**Fold path** — same-node background materializer, off the ack path:

```
materializer
   │
   ▼  per dirty actor A
Replica.Sync(ctx)  == UPLOAD
   · remote pos:  LIST actors/A/ltx → max TXID     (or use seeded SetPos)
   · local pos:   A.Pos() = max TXID in local L0 files
   · for txid in (remote, local]:
        open  .<A>-litestream/ltx/0/<txid>.ltx     (local file)
        PUT   → actors/A/ltx/<txid>.ltx            (1 PUT per L0)
   │
   ▼
actors/A/ltx/... contiguous  →  restore-complete
   │
   ▼
when watermark(A) ≥ bundle.max_txid(A) AND compacted → bundle eligible for GC
```

Rules: folds must run **before local L0 cleanup**, and folds are idempotent (PUT same-key-same-bytes).

**Restore path** — activation, including takeover on another node:

```
activate(A)
   │
   ▼  source selection (celld-style)
┌──────────────────────────────┐
│ local eviction snapshot      │── reuse if !takeover (cheapest)
│ (prev epoch, .evicted)       │
└──────────────┬───────────────┘
               │ no usable local copy
               ▼
[fold A's acked tail]  bundle → prefix        (only un-folded txids)
   for each retained bundle, A's segments:
     PUT bytes → actors/A/ltx/<txid>.ltx
   (the successor has no local L0 files,
    so this tail fold is a direct bundle→prefix PUT)
               │
               ▼
Replica.Restore (or paged restore)
   · plan = latest snapshot + contiguous L0/L1 chain from actors/A/ltx/
   · download + apply → local db file (or fault-in VFS paging)
               │
               ▼
open db, seed_continuation (continue the chain) → serve
```

Invariant: an acked write is always either in the folded prefix or in a retained bundle — the restore-path tail fold covers the gap.

### 9.4 Strategy B — custom materializer (when you want lazy folds)

Skip the per-actor `Replica.Sync`; the materializer PUTs bundle segments directly to the prefix keys and tracks a per-actor **folded-watermark** (highest folded txid).

- **Key derivation must match litestream exactly** (`ltx_file_path`, level dirs, snapshot paths, generation marker) or stock restore breaks. This is the main cost of Strategy B.
- **Benefit:** folds can be fully demand-driven — an actor that stays local and un-evicted may never need its prefix folded; the bundle is its durable record. Saves per-actor PUTs for cold/resident actors.
- Requires the same invariants as Strategy A (ordering, idempotency, restore-complete).

Recommendation: start with **Strategy A** (correct by construction), and only add B if measurement shows resident actors' fold PUTs are worth deferring.

### 9.5 Ordering and idempotency

- **Order:** the bundle serializes segments as `(actor, txid)` ascending; the fold writes per-actor segments in that order. litestream restore applies segments in filename/txid order, so this must be preserved.
- **Idempotency:** a fold is PUT-same-key-same-bytes. Concurrent or duplicate folds are harmless; two nodes folding the same bundle converge.
- **Restore-complete invariant:** actor A's prefix is safe to restore only when it holds a **contiguous** chain through the newest durable cut for A (litestream tolerates no gap in an L0 chain — a missing segment forces a full snapshot, or a failed restore). The watermark plus bundle retention guarantees that any segment not yet folded is still present in a retained bundle.

### 9.6 Retention / GC boundary

- A bundle is deletable only when, for **every** actor it covers, `folded_watermark(actor) ≥ bundle.max_txid(actor)` **and** the folded chain has been compacted past that point.
- Folds must precede GC: deleting a bundle whose segments were never folded loses acked data (the "dirty tails" rule — celld refuses to evict/restore until the tail is folded).

### 9.7 Epoch fencing

- Bundle headers carry `epoch`; folded objects are scoped under `actors/<pid>/e<epoch>/` (matching the per-actor prefix scheme).
- The materializer refuses a segment whose epoch is not the current owner's — a fenced writer's segments must never be materialized into a live chain.
- This is the mitigation for the "no distributed coordination" non-goal: single-writer safety is a property of the bucket (CAS ownership record + epoch in prefix), not a hope about the deployment.

### 9.8 Failure semantics

- A failed fold PUT retries; the segment stays in the bundle (retention covers it) and the actor's watermark does not advance.
- A failed bundle PUT is the ack failure — waiters get the store's failure path (queued-vs-stall budget, Section 8.6), and the committer falls back to the per-actor prefix upload for that flush.
- On crash between bundle PUT and fold, the fold re-runs from the retained bundle on the next drain/takeover — the bundle is the durable, self-describing record of exactly what to fold.

### 9.9 Fold summary

| Concern | Answer |
|---|---|
| What is folded | The already-encoded L0 LTX segment bytes from the bundle |
| Where it goes | The actor's per-actor S3 prefix, at litestream's exact object keys |
| What is NOT touched | The local SQLite file / WAL (data is already committed) |
| Recommended mechanism | `Replica.Sync` for same-node folds; bundle-aware materializer for takeover tails |
| When | Periodic drain, eviction, and before takeover-restore |
| Ordering | Per-actor txid ascending, as the bundle serialized it |
| Idempotency | PUT same-key-same-bytes; concurrent folds converge |
| GC boundary | Folded watermark ≥ bundle max txid AND compacted, per actor |
| Fencing | Segments scoped by epoch; refuse stale-epoch folds |
| Restore | Prefix is restore-complete only with a contiguous folded chain |

## 10. Concurrency & Consistency

- **Per-actor serialization:** one writer at a time per actor DB (`SetMaxOpenConns(1)` + `BEGIN IMMEDIATE`), matching eGo's sequential per-actor writes.
- **Append-only integrity:** the `(persistence_id, sequence_number)` PK rejects duplicate sequence numbers; a write batch is atomic (all-or-nothing).
- **Upsert semantics:** `WriteState` is idempotent via `ON CONFLICT DO UPDATE`.
- **Cross-actor isolation:** different actors never share a lock or file — zero cross-actor lock contention, the key benefit of "one SQLite instance per actor."
- **Monotonicity (optional):** reject out-of-order versions/sequences (compare against the latest before insert) for fail-fast; the PK provides the hard guarantee.
- **Epoch fencing (multi-node):** see Section 9.7.

## 11. Trade-offs: one SQLite instance per actor

### Advantages
- **Isolation & no contention:** each actor's writes are independent; no global write lock on a shared DB.
- **Granular restore/delete:** recover or GC a single actor's state in S3 without touching others.
- **WAL checkpoint isolation:** a large actor's WAL does not force checkpointing of others.
- **Simple failure domain:** a corrupted DB only affects one actor.
- **`PersistenceIDs`** is a free directory listing.

### Disadvantages / risks
- **Many files / many replicas:** N actors → N DB files + N S3 prefixes; S3 object counts multiply (mitigated by compaction + fold GC).
- **Cross-actor queries** (`GetShardEvents`, `ShardNumbers`) require fan-out across actor DBs — the interface was designed for a shared table. Costs grow with actor count.
- **File descriptor pressure:** N open SQLite files if all actors are active. Mitigated by an LRU cache of actor DBs.
- **Not suited for very high actor counts in a single process** — per-file and per-`litestream.DB` overhead becomes non-trivial beyond thousands of actors. A shared-DB mode is a follow-up.
- **`DeleteEvents` space in S3** is reclaimed only by retention, not immediately.

## 12. Config

```go
type Config struct {
    DataDir          string        // root directory holding per-actor DBs/journals (required)
    BucketURL        string        // S3 base URL, e.g. "s3://my-bucket/ego-state" (required)
    Endpoint         string        // optional custom S3 endpoint (MinIO/compatible stores)
    Region           string        // optional S3 region override

    SyncMode         SyncMode      // SyncOnWrite | GroupCommit (default) | AsyncInterval
    SyncInterval     time.Duration // interval for AsyncInterval mode (0 when not used)
    SnapshotInterval time.Duration // compaction/snapshot policy, if supported by pinned version
    RetainDuration   time.Duration // application/object-store GC policy, if enabled
    MaxOpenDBs       int           // LRU cap for concurrently open actor DBs (default 128)

    GroupCommit      GroupCommit   // group-commit knobs (Section 8.7)
}
```

`NewDurableStore(cfg)` returns a `*DurableStore` implementing `persistence.StateStore`; `NewEventsStore(cfg)` returns an `*EventsStore` implementing `persistence.EventsStore`.

## 13. Implementation sketch (Go)

```go
package sqlite

import "github.com/benbjohnson/litestream"

type actorDB struct {
    sql     *sql.DB          // driver handle (modernc.org/sqlite or mattn/go-sqlite3)
    replica *litestream.Replica
    lsDB    *litestream.DB
}

type DurableStore struct { // EventStore is analogous, plus a shard cache
    cfg       *Config
    mu        sync.Mutex
    actors    map[string]*actorDB  // LRU cache keyed by persistenceID
    committer *committer           // group-commit coordinator (Section 8)
    materializer *materializer     // fold workers (Section 9)
    connected bool
}

func (s *DurableStore) Connect(ctx context.Context) error {
    // mkdir -p DataDir; verify bucket reachable; start committer + materializer
}

func (s *DurableStore) Disconnect(ctx context.Context) error {
    // final barrier flush (prove pending tickets), stop committer/materializer,
    // checkpoint WAL, close lsDBs
}

func (s *DurableStore) WriteState(ctx context.Context, st *egopb.DurableState) error {
    adb := s.actorDB(ctx, st.GetPersistenceId())  // open + migrate + restore-if-missing
    // INSERT ... ON CONFLICT DO UPDATE
    return s.committer.captureAndWait(ctx, adb)   // capture + bundle + await proof
}

func (s *DurableStore) GetLatestState(ctx context.Context, id string) (*egopb.DurableState, error) {
    adb := s.actorDB(ctx, id)                      // may trigger Restore from S3
    // SELECT ... ; return nil when no row
}
```

- `actorDB(ctx, pid)` opens the SQLite file, sets WAL pragmas, builds the litestream `Replica`, and restores from S3 when the local file is absent and a replica exists.
- Driver: `modernc.org/sqlite` (pure-Go, no CGO) or `mattn/go-sqlite3` (CGO). Prefer `modernc.org/sqlite`; decision point (Open Questions #1).
- Row unmarshalling reuses the `ToDurableState` / `ToEvent` / manifest pattern from the postgres stores' `row.go`.

## 14. Module layout

```
durablestore/sqlite/            eventstore/sqlite/
  durable_store.go                events_store.go
  actor.go                        journal.go
  committer.go                    committer.go      (shared plumbing)
  materializer.go                 materializer.go
  syncer.go                       syncer.go         (AsyncInterval mode)
  config.go                       config.go
  row.go                          row.go
  crossactor.go                   crossactor.go     (PersistenceIDs/ShardNumbers/GetShardEvents)
  resources/                      resources/
  README.md                       README.md
  Earthfile                       Earthfile
  go.mod                          go.mod
```

The modules ship as `github.com/tochemey/ego-contrib/durablestore/sqlite` and `github.com/tochemey/ego-contrib/eventstore/sqlite`. If the shared plumbing (file layout, sync modes, restore, LRU, committer/materializer) grows, extract an internal `sqlitekit` package rather than duplicating it (Open Questions #5).

## 15. Testing strategy

- **Unit tests:** upsert semantics; `GetLatestState` on empty DB returns nil; append-only PK violation on duplicate sequence numbers; `WriteEvents` atomicity; `DeleteEvents` boundary (inclusive); `ReplayEvents` range ordering; per-actor isolation (two actors write independently); manifest round-trip.
- **Cross-actor tests (event store):** `PersistenceIDs` pagination via directory listing, `ShardNumbers` union + cache invalidation, `GetShardEvents` merge/sort/limit with multiple actors in the same shard.
- **Integration (Testcontainers-Go + MinIO, matching sibling modules):** WAL produced and synced to MinIO; `Replica.Restore` recreates an actor's full state/journal after local files are deleted; retention/snapshot behavior honored.
- **Group commit:** bundle is the ack target; barrier waiters wake only after bundle proof; fold (Strategy A) materializes prefixes; bundle GC only after watermark + compaction; restart between bundle and fold re-folds correctly.
- **Restart/replay:** write, "lose" local data, restore from MinIO, verify full replay returns the same ordered events.

## 16. Operational notes

- No separate daemon: replication is in-process. `GroupCommit` gives RPO=0 with amortized cost; `SyncOnWrite` gives the smallest per-write latency; `AsyncInterval` trades durability window for throughput.
- Back up the S3 bucket itself for extra safety (Litestream is not a substitute for a full backup strategy).
- Local DB files are ephemeral; on a fresh node, restore happens automatically from S3 on first access.
- S3 credentials come from the standard AWS credential chain (env vars, shared config, IAM role).
- For journals, configure the pinned version's compaction and object-store GC policy to cover the longest expected replay/audit window — `DeleteEvents` does not immediately free S3 space.
- `GetShardEvents` cost grows with actor count; monitor if the process hosts many actors.

## 17. Open questions

1. Driver choice: `modernc.org/sqlite` (pure Go) vs `mattn/go-sqlite3` (CGO) — affects cross-compilation and the module's `go.mod`/Earthfile.
2. Monotonicity guard: reject out-of-order versions/sequences (fail-fast) or trust eGo to sequence writes?
3. `DeleteEvents`: physically delete rows (postgres parity) or tombstone them (`is_deleted`) to preserve replay history?
4. Scale ceiling for the fan-out `GetShardEvents` — a follow-up shard-index DB (or shared journal DB per shard) may be warranted at high throughput.
5. Reuse of shared litestream/sqlite plumbing between the two modules (internal `sqlitekit` package) vs duplication.
6. Fold laziness: is Strategy A's periodic fold sufficient, or do resident actors need Strategy B's demand-driven folds (measure first)?
7. Shard cache invalidation: invalidate on every write vs a short TTL — a hot writer could thrash the cache.
