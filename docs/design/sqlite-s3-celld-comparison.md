# Comparison: ego-contrib SQLite/Litestream proposal vs celld's SQLite-in-S3

- **Proposals analyzed:**
  - `docs/design/sqlite-litestream-durable-state.md` (durable state)
  - `docs/design/sqlite-litestream-event-sourcing.md` (event-sourced journal)
- **Compared against:** [celld](https://github.com/denoland/celld) at `v0.4.1` — a Cloudflare-Workers-compatible runtime whose "cell" is a Durable-Object-style unit of stateful computation, each backed by its own SQLite database replicated to an S3-compatible bucket.
- **Status:** Analysis / design review

## 1. What celld is (and why it is the right thing to study)

celld is a production-grade, already-shipped implementation of almost exactly the design the ego-contrib proposals describe:

- **One SQLite file per actor.** In celld the actor is a "cell"; `crates/celld/storage.rs` states it directly: *"Each cell is its OWN db file (its own replicated, epoch-fenced bucket prefix)"*.
- **In-process WAL replication to S3 — no external daemon.** The `celld-ltx` crate (`crates/ltx`) is a from-scratch Rust reimplementation of Litestream v0.5 + the LTX file format (seeded from the `rustyriver` project), wired in-process (`crates/celld/ltx_repl.rs`): *"No external process, no directory-watch lag"*.

So celld validates both core decisions in the ego-contrib proposals — per-actor SQLite and the embedded replication library — at real scale (thousands of cells per node, 2 GB "whale" cells, 4,000 cells committing in the same second). It also solves several problems the proposals either leave open or mark as non-goals. The comparison below is the production answer to every open question in the proposals.

## 2. How celld stores state in SQLite-in-S3

### 2.1 Object and file layout

- Local: `<watch>/<cell>/ltx/e<epoch>/db.sqlite` (+ `-wal`, `-shm`).
- Bucket: `cells/<cell>/ltx/e<epoch>/` — **the epoch is part of the prefix**, which is the *data-path fence* (`crates/celld/replication.rs:3`): *"a stale owner writes a dead prefix."*
- One shared `object_store` client per node (`crates/celld/bucket.rs`) over S3/GCS/Azure (`s3://`, `gs://`, `az://`), with a fleet key prefix so multiple fleets share one bucket. Conditional-write CAS tokens differ per dialect (S3/Azure etag vs GCS generation) but are unified behind one opaque token.

### 2.2 Replication (`celld-ltx`)

- A managed `celld_ltx::Db` per resident cell captures the committed WAL into **L0 LTX segments** (`crates/ltx/src/db.rs`). It ports Litestream's mechanics faithfully:
  - WAL mode with `wal_autocheckpoint(0)` — Litestream (not SQLite) owns checkpointing.
  - A **dedicated connection holding a long-running read transaction** that takes over checkpointing (the "checkpoint takeover"), plus a main connection for writes/pragmas — mirroring Litestream's `rtx` pool behavior.
  - Control tables `_litestream_seq` / `_litestream_lock` inside every db (WAL bootstrap + write-lock; `db.rs:331`).
  - The `verify` snapshot-on-continuity-break lattice, WAL checksum validation (`crates/ltx/src/wal.rs`), and a **3-tier checkpoint policy** (page-count thresholds, time-based, anti-feedback flags).
- **Compaction:** a node-wide scheduler publishes additive L1 files, and a `ReplicaCompactor` (`crates/ltx/src/replica_compactor.rs`) compacts LTX levels up to the L9 snapshot level. This is how celld keeps the *number of S3 objects* bounded — the exact trade-off the ego-contrib proposals flag ("S3 object counts multiply").

### 2.3 Durability contract (the part the proposals lack)

celld does not just call `sync()` after a write and hope. It has an explicit **ack-gated durability protocol**:

- A write takes a **durability ticket** (`crates/logic/durability.rs`). The **output gate** holds the write's external effect until a WAL capture whose `capture_seq` reaches that ticket is **proven durable in the bucket**.
- The wait distinguishes a *queue* (healthy node, more writes than upload slots; waits extend) from a *stall* (stuck upload; fails in one fixed budget), so bursts drain without false failures and real stalls still time out (`durability.rs:3-24`).
- Effect: **RPO = 0 for acknowledged writes** — a write the client saw succeed is already in the bucket. A crashed cell's committed-but-unacked writes are discarded on the next epoch, which is deliberate: "RPO=0 covers exactly the acknowledged ones" (`crates/logic/restore.rs:12`).
- There is also a **log tier** (`crates/celld/node_log.rs`): captured L0 segments across *all dirty cells* are folded into **bundles** — one PUT per node per flush interval (`crates/celld/ltx_repl.rs:188-215`). This is group-commit across actors: amortizes S3 PUT cost and is the fleet-durable log; the per-cell prefix upload is the fallback.

### 2.4 Ownership / epoch fencing

- Cell ownership is a **conditional-write record carrying an epoch** in the bucket (`crates/celld/ownership_store.rs`). Taking ownership is a CAS create/update; the epoch is stamped into the LTX prefix.
- Activation variants: `fresh` (epoch 1, conditional create — no prior replica), `took_over` (seized from another node — only the *other* node's durable state is authoritative), `resume_local` (clean local reload continues the same epoch).
- This is the answer to the proposal's non-goal "no distributed coordination." celld makes single-writer safety a *property of the bucket* (CAS + epoch), not a hope about the deployment.

### 2.5 Restore

- **Source selection** (`crates/logic/restore.rs`): prefer a preserved **local eviction snapshot** from the *previous* epoch — but only when NOT a takeover (a takeover means someone else may have written while we slept). Otherwise restore the **newest non-empty durable epoch's contiguous LTX chain** (`crates/celld/ltx_repl.rs:1465-1503`).
- **Paged restore:** for large chains, instead of downloading everything, celld builds a page map from the LTX page indexes and opens the DB through a **fault-in SQLite VFS over an empty local file** — pages are fetched on first use, and the new epoch *continues the paged-in chain* (`seed_continuation`) rather than re-snapshotting (`ltx_repl.rs:1640-1740`). This directly answers the proposal's open question #5 (synchronous vs background restore): restore happens **at activation, before the cell serves**, and for big DBs it pages instead of blocking on a full download.

### 2.6 SQLite schema and hardening

- Tables mirror Cloudflare's Durable Object storage: `_cf_KV`, `_cf_ALARM`, `_cf_METADATA`, plus `_cf_FACETS` (nested "facet" DO images stored as BLOBs in the root db) (`storage.rs:395-417`).
- WAL pragmas: `journal_mode=WAL`, **`synchronous=NORMAL`** (not FULL — replicated-WAL convention; measured 1.4 ms → 19 µs per put), `busy_timeout`. `synchronous=OFF` only while the schema is created, to avoid a cold-cell fsync bottleneck (`storage.rs:358-443`).
- **SQL authorizer**: reserves `_cf_*` and `_litestream_*` names from application SQL; denies `ATTACH`/`VACUUM`/`BEGIN`/`SAVEPOINT`/temp objects and `load_extension`; allowlists `fts5`/`vec0`; caps SQLite resource limits. VACUUM is blocked because it is a whole-file rewrite against a replicating db (`storage.rs:529-661`).
- **Failure taxonomy** (`crates/logic/sqlite.rs`): a critical engine error (`FULL/IOERR/NOMEM/INTERRUPT`) that destroys an active transaction *poisons the actor* (fail closed); a statement-level error or a spared transaction recovers. This is the error handling the proposals never mention.

## 3. Point-by-point comparison

| Aspect | ego-contrib proposal | celld (v0.4.1) | Verdict |
|---|---|---|---|
| Granularity | 1 SQLite per actor (persistenceID) | 1 SQLite per cell | **Same**; celld proves it at scale |
| Replication engine | embedded `benbjohnson/litestream` (Go) | in-process Rust port of Litestream/LTX (`celld-ltx`) | Same shape; celld reimplemented to own it |
| Object layout | `s3://bucket/actors/<pid>/` | `cells/<cell>/ltx/e<epoch>/` | Same, plus epoch |
| Sync-on-write | `WriteState` calls `db.Sync()` before returning | write acked only after bucket proof (output gate) | Proposal weaker: no ack contract |
| Batched sync | optional `SyncInterval` background loop | log tier + bundles (one PUT per node per interval) | celld's is cross-actor group commit |
| Ownership / fencing | non-goal | conditional-write ownership record + epoch-in-prefix | **Gap** — needed for multi-node safety |
| Restore | full download on first access | local snapshot reuse → chain restore → **paged fault-in VFS** | celld strictly better |
| Compaction / object count | snapshot interval + retention only | L0→L1→L9 additive compaction, replica compactor | celld bounds S3 object count |
| Retention | `RetainDuration` (litestream) | not implemented; compaction + GC instead | celld relies on compaction, not retention |
| Cross-actor queries | fan-out across actor DBs (EventsStore) | none needed (per-cell storage API) | Different interface; celld avoids the problem |
| Schema | `states_store` / `events_store` tables | `_cf_KV`, `_cf_ALARM`, `_cf_METADATA`, `_cf_FACETS` + `_litestream_*` | Mirror their persistence model |
| SQLite hardening | not addressed | authorizer, reserved names, VACUUM deny, resource limits | **Gap** for a multi-tenant runtime |
| Failure handling | not addressed | poison-on-critical-engine-error taxonomy | **Gap** |
| Multi-backend | S3 only | S3/GCS/Azure with per-dialect CAS | celld broader |
| Durability position | `version_number` column | committed-write position = total_changes + data_version + schema_cookie + epoch | celld explicit; proposal implicit |
| Backend | Go + litestream | Rust, owns the format | For ego-contrib, reusing litestream is correct |

## 4. What celld validates in the proposal

These are no longer speculative:

1. **"One SQLite instance per actor" is the right call.** celld runs thousands of cells per node on that model; per-actor isolation and granular restore are real, and per-cell write lock contention is zero.
2. **The embedded-library approach is production-proven.** No external daemon; capture and upload are in-process and registered the instant a cell activates, so even a fresh cell can be proven durable with no cold-start window.
3. **`synchronous=NORMAL` + WAL is the right pragma set** for a replicated-WAL system; durability comes from replication, not from local fsync.
4. **Per-actor S3 prefixes enable cheap discovery and pagination** — celld lists cells via delimiter listings (`common_prefixes`), which is exactly the `PersistenceIDs` = directory-listing idea.

## 5. Gaps the proposal should close (the production answer to its open questions)

The proposals' open questions map 1:1 to decisions celld already made:

1. **Ownership / fencing (the biggest gap).** A local-actor store is fine for single-node, but the moment an actor can move nodes (restart, failover, scale-out) you need the bucket itself to arbitrate ownership. Adopt: a **CAS ownership record per actor carrying a monotonically increasing epoch**, and stamp the epoch into the S3 prefix. Restores must select the newest durable epoch, and a stale writer must write only a dead prefix. *(Proposal non-goal "no distributed coordination" should be revisited — celld shows it is cheap to add and essential.)*
2. **Ack-gated durability.** Define the durability contract as: *`WriteState` returns only after the covering WAL segment is durable in the bucket* (and `GetLatestState`/`ReplayEvents` may observe only what is bucket-proven, plus unproven local writes bounded by the current activation's baseline). This turns "SyncOnWrite" into a real RPO=0 guarantee instead of a best-effort call.
3. **Group commit across actors.** Rather than a per-actor background sync, coalesce captured segments from all dirty actors into periodic bundle PUTs (the log tier), with the per-actor prefix upload as the fallback. This is the economically correct answer to "N actors → N S3 round trips."
4. **Paged restore.** Replace "restore = full download, blocking first access" with a fault-in VFS that pages the actor's DB in on first use, continuing the LTX chain instead of re-snapshotting. This closes open question #5 (sync vs background restore) with a third, better answer.
5. **Compaction, not just retention.** Add L0→L1→L9 additive compaction (or enable Litestream's) so the S3 object count is bounded independently of retention; keep retention for the replay window.
6. **SQLite hardening.** If the store can run arbitrary user SQL (eGo actors only run store-invoked SQL, so this is lighter than celld's), at minimum: reserve control-table names, deny `ATTACH`/`VACUUM`, and define a critical-error → fail-closed policy so a poisoned actor never serves stale state.
7. **Restore source selection.** Reuse a preserved local snapshot from the previous epoch when it is provably safe (not a takeover); otherwise restore the newest non-empty durable epoch.

## 6. What celld has that the proposal does not need

- **`_cf_FACETS` / embedded facet DBs** — a Cloudflare DO-specific feature (nested DO images), irrelevant to eGo.
- **Event-sourcing journal with cross-actor queries** — celld's storage API is per-cell KV/alarms, so it never faces the `EventsStore` fan-out problem. The ego-contrib `eventstore` proposal's `GetShardEvents`/`ShardNumbers` fan-out remains a consequence of the eGo interface, and celld offers no precedent to copy (it simply doesn't have such queries).
- **R2 binding primitives** (Worker blob storage) — out of scope.

## 7. Recommendations (concrete, in priority order)

1. **Add epoch-fenced ownership to both proposals** (durable state first, then eventstore). One ownership object per actor: `{node, epoch}` updated via CAS; epoch in the S3 prefix. This is the single change that takes the design from "single-process local store" to "safe distributed store."
2. **Re-specify SyncOnWrite as ack-gated durability** with a ticket/proof contract and an explicit failure budget, rather than a bare `db.Sync(ctx)` call.
3. **Adopt a bundle/log tier** for cross-actor group commit before building the per-actor background sync loop.
4. **Plan for compaction** from the start (L1 additive + periodic full snapshot) to bound object count.
5. **Implement paged restore** as the answer to the restore-blocking question.
6. **Add SQLite hardening** (reserved names, VACUUM/ATTACH denial, fail-closed error taxonomy) before shipping a multi-actor runtime.

## Appendix: key celld sources

- In-process replication + object layout: `crates/celld/ltx_repl.rs` (module doc, `activate`)
- Epoch-in-prefix fence: `crates/celld/replication.rs`
- Per-cell DB + schema + authorizer: `crates/celld/storage.rs`
- Durability ticket/proof: `crates/logic/durability.rs`
- Ownership records / CAS: `crates/celld/ownership_store.rs`, `crates/celld/bucket.rs`
- Restore source predicates: `crates/logic/restore.rs`
- LTX/WAL port (Litestream): `crates/ltx/src/{db,wal,replica_compactor,replica}.rs`
- Failure taxonomy: `crates/logic/sqlite.rs`