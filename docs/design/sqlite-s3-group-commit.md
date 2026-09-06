# Group Commit Design: amortizing S3 replication across actors

- **Applies to:** `docs/design/sqlite-litestream-durable-state.md`, `docs/design/sqlite-litestream-event-sourcing.md`
- **Inspired by:** celld's log-tier bundles (`crates/celld/node_log.rs`, `crates/celld/ltx_repl.rs`) and its ack-gated durability (`crates/logic/durability.rs`), analyzed in `docs/design/sqlite-s3-celld-comparison.md`
- **Status:** Proposal (options + recommendation)

## 1. Problem

With one SQLite file per actor, naive replication is one S3 upload per actor write:

- **SyncOnWrite:** each `WriteState`/`WriteEvents` → one `db.Sync(ctx)` → one segment upload. A node with `N` actors writing at `W` writes/s does `N·W` S3 PUTs/s. S3 bills per request; small segments pay full request cost.
- **SyncInterval:** a background loop reduces *timing* fragmentation but still uploads one object per actor per tick — no cross-actor sharing. PUT count stays `O(active actors / tick)`.

Group commit fixes both: **many writes, across many actors, share one upload and one durability ack.**

## 2. Design goals

1. Cut the S3 PUT rate by 1–2 orders of magnitude under write fan-out.
2. Keep a defined RPO: either RPO=0 (ack-gated, writes block until bucket-proof) or a bounded window (optimistic return + periodic barrier).
3. Preserve the per-actor object layout so restore stays granular and cheap.
4. Preserve epoch fencing: a fenced writer must not corrupt the group.

## 3. Option 1 — Barrier group commit (temporal, per-node single uploader)

A single **committer** owns all replication uploads. The write path never touches S3:

```
WriteState / WriteEvents
   │  (local SQLite commit — fast, no S3)
   ▼
commit log: per-actor pending queue (captured L0 segment, dirty flag)
   │
   ▼  every group_interval, or when bytes/actors exceed a threshold:
committer: capture WAL of every dirty actor → one L0 segment each
   │  upload with bounded parallelism (e.g. 8–16 slots)
   ▼
bucket: per-actor prefixes <pid>/<segment> (unchanged layout)
```

- **Synchronous variant (recommended):** each write's call blocks until the *covering* upload — the segment for its actor in the next flush — is proven durable. Writes within one interval share one flush and one ack, so effective latency ≈ `group_interval + upload`, amortized. This is PostgreSQL/InnoDB-style barrier group commit and gives **RPO=0**.
- **Optimistic variant:** `WriteState` returns after the local commit; a background committer proves durability within `group_interval`. Cheapest per-write latency; RPO = `group_interval`. Needs a barrier (`Disconnect`, or a future explicit `Sync`) for clean shutdown.
- **Coalescing bonus:** many writes to the *same* actor inside one interval collapse into a single segment — the fix for both durable-state (1 row/actor) and eventstore (append batches).

**Trade-offs:** per-actor prefix uploads still cost one PUT per actor per flush — the *number* of PUTs scales with active actors per interval, not with writes. Fine for small `N`; the bundle tier (Option 2) removes the last per-actor PUT.

## 4. Option 2 — Bundle log tier (true cross-actor single PUT)

Folds every dirty actor's captured segment into **one bundle object per flush**:

```
bucket layout (addition):
  bundles/<node-id>/<seq>.bundle      ← the ack target, one PUT per interval
  actors/<pid>/...                    ← per-actor prefix, now a restore/tier target only

bundle payload:
  header: sequence, epoch, [ (actor, epoch, min_txid, max_txid, offset) ... ]
  body:   concatenated L0 segments, in (actor, txid) order
```

- The **durability proof is the bundle object**: a write's ticket names `(bundle_seq, offset)`. All actors included in the flush are proven by the one PUT.
- PUT rate drops to `~1 per interval per node` regardless of how many actors wrote — the "N actors × W writes" cost collapses to `W/interval` objects (often 10–50/s).
- **Restore stays per-actor:** a successor reading actor A gathers A's segments — first from A's own prefix, then **folding** A's tail out of retained bundles (`fold_cell` in celld) — before/while restoring. The per-actor prefix remains the durable, restore-able truth; bundles are an accelerator, not the source of truth.
- **Retention/GC:** after an actor's segments are materialized into its prefix and compacted, bundles older than the newest materialized cut are deleted.

**Trade-offs:** restore of an actor whose newest tail lives only in bundles needs the gather step (one extra pass); followers must understand the bundle format; more moving parts than Option 1.

## 5. Option 3 — Hybrid (recommended)

Combine both:

1. **Write path = barrier group commit.** Local commit, enqueue, wait for covering proof. RPO=0, one ack per flush, per-actor coalescing.
2. **Upload path = bundle tier with per-actor fold.** The committer writes one bundle per interval (the ack target). A bounded set of materializer workers folds each actor's segments into its per-actor prefix **concurrently with serving**, and compaction (L1/L9) runs against the prefix.
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

**Why this shape:** the ack path never touches the per-actor prefixes, so PUT cost is ~1/interval; the restore path never touches bundles, so restore stays granular and celld-style source selection (local snapshot → chain → paged) is unchanged.

## 6. Correctness properties to hold

- **Ordering:** segments within a bundle are ordered by `(actor, txid)`; a bundle has a monotone sequence; a write's ticket = `(bundle_seq, offset)`. A flush is atomic: it either covers all listed actors or none.
- **Durability proof:** a proof for `bundle_seq` covers every write whose ticket `≤ (seq, offset)`. Proof is *idempotent* — a proof for a later bundle implies all earlier ones (so a node can skip redundant PUTs of the same segment).
- **Epoch fencing:** bundles carry the owning epoch and live under `bundles/<node>/<epoch>/` (or the segment headers carry it); a fenced writer's bundles write a dead prefix. Materialization must refuse a segment whose epoch is not the current owner's.
- **Retention boundary:** never delete a bundle still referenced by an un-materialized actor's newest cut; GC only after fold + compaction confirms the prefix is ahead.
- **Failure semantics (celld's lesson):** the wait distinguishes a *queue* (healthy node, uploads behind) from a *stall* (stuck upload). Extend waits while the node lands proofs, fail a stuck upload on a fixed budget — not a fixed deadline from each write's commit.
- **Empty flushes:** emit no bundle when nothing is dirty (zero-write nodes pay nothing).

## 7. Knobs (Config additions)

```go
type GroupCommit struct {
    Interval       time.Duration // flush cadence; also the ack latency floor (default 10ms)
    MaxBatchBytes  uint64        // flush early when a batch exceeds this (default 4 MiB)
    MaxActors      int           // flush early when too many actors are dirty (default 256)
    UploadSlots    int           // parallel segment/bundle uploads (default 8–16)
    Bundle         bool          // enable the bundle tier (default true)
    MaterializeWorkers int       // concurrent fold workers (default 4)
    WaitBudget     time.Duration // proof deadline per flush (default 30s)
}
```

The `SyncMode` enum becomes: `SyncOnWrite` (legacy), `GroupCommit` (barrier, RPO=0), `AsyncInterval` (optimistic, RPO=interval).

## 8. eGo interface impact

- `WriteState` / `WriteEvents` in **GroupCommit** mode block until the covering proof lands → semantics equivalent to today's `SyncOnWrite`, but amortized. This is the safe default.
- `Disconnect` performs a final barrier flush (prove all pending tickets) before closing DBs — preserves durability across clean shutdown.
- `GetLatestState` / `ReplayEvents` are unaffected: reads are local. Optional: a read-only answer may report how fresh it is (celld's `observed_position`), but that is additive.

## 9. Recommendation

Adopt **Option 3 (hybrid)** as the default in both proposals, with `SyncOnWrite` kept for latency-critical single-actor tests. Order of work:

1. Barrier committer + per-actor coalescing (Option 1) — biggest win, least machinery.
2. Bundle tier (Option 2) as the ack target + fold — removes per-actor PUTs at scale.
3. Materialize-on-restore + bundle GC — closes the lifecycle.

This mirrors celld's proven production shape (log-tier bundles for the ack, per-cell prefixes for restore) while staying a straightforward extension of the embedded-Litestream design.

## 10. Folding the bundle into per-actor replicas

The bundle is the ack target; the per-actor S3 prefix is the restore/tiering target. **Folding** is moving each actor's captured segments from the bundle into that actor's prefix.

### 10.1 What "fold" means (and does not mean)

- **It is NOT applying SQL.** Each bundle entry is already the exact L0 LTX segment bytes that a litestream replica upload would have produced (the per-actor `DB` captured the WAL, encoded the segment, and handed the bytes to the committer). The local SQLite file already holds the committed data — nothing is replayed, no second SQLite is opened.
- **It IS writing objects.** For each actor, PUT the segment bytes to the actor's prefix at the standard litestream LTX key, in txid order, plus the generation/snapshot metadata a litestream restore expects. After the fold, the actor's prefix is byte-for-byte a faithful litestream replica, so stock `restore` reads it.

```
bundle bundles/<node>/<seq>.bundle            actors/<pid>/ltx/<min>-<max>.ltx (L0)
   ┌────────────┬────────────┐                 actor A: min_a-max_a, min_b-max_b, ...
   │ actor A    │ segment a  │  ── fold ──▶   actor B: ...
   │ actor B    │ segment b  │                (exactly the keys litestream derives:
   │ ...        │ ...        │                 ltx_file_path / level dirs / snapshots)
   └────────────┴────────────┘
   (ack target: 1 PUT/flush)                   (restore + compaction read here)
```

### 10.2 When folds happen

Folding is background and eventually consistent — it is **not** on the ack path. Three triggers:

1. **Periodic drain:** a materializer walks retained bundles in `seq` order and folds segments for actors whose folded-watermark lags.
2. **Eviction:** before a node evicts an actor, fold its tail so any successor can restore from the prefix alone (a local snapshot may then be written and the prefix is the remote truth).
3. **Takeover / restore:** a successor that must restore actor A folds A's tail out of retained bundles *before* reading the prefix — exactly celld's `fold_cell` before `epoch_chain` (`crates/celld/ltx_repl.rs`). This is the hard requirement: an acked write must be in either the prefix or a retained bundle at restore time.

### 10.3 Strategy A — litestream's own replica sync is the fold (recommended)

Instead of inventing a folder, reuse litestream's upload path:

- The per-actor `DB` keeps capturing L0s into its local meta dir (`.db-litestream/ltx/...`) as usual. The committer **reads those freshly-captured bytes** to build the bundle — so the bundle holds exactly what the replica would upload.
- The ack waits on the bundle object (`bundle_seq`). The per-actor `Replica.Sync(ctx)` — litestream's own uploader — is then invoked in the background by the materializer. It uploads the captured L0s to the actor's prefix at the keys litestream derives, including generation metadata.
- **Result:** zero risk of key-layout drift. The fold is stock litestream behavior; the bundle is a co-written group ack object that happens to hold the same bytes.

```
per-actor DB (litestream)
   capture → L0 bytes
        │  (a) committer reads them → bundle (ack target)
        │  (b) materializer later → Replica.Sync → actors/<pid>/ltx/...
```

Constraint: the bundle PUT for a flush must complete **before** the waiters for that flush wake (ack), and the materializer's replica sync for those segments must not delete anything the bundle references (it doesn't — uploads are additive).

#### 10.3.1 The three paths (Strategy A)

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

Rules: folds must run **before local L0 cleanup** (`Replica.Sync` opens the local file; if it is gone, that segment falls back to the bundle bytes), and folds are idempotent (PUT same-key-same-bytes).

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

### 10.4 Strategy B — custom materializer (when you want lazy folds)

Skip the per-actor `Replica.Sync`; the materializer PUTs bundle segments directly to the prefix keys and tracks a per-actor **folded-watermark** (highest folded txid).

- **Key derivation must match litestream exactly** (`ltx_file_path`, level dirs, snapshot paths, generation marker) or stock restore breaks. This is the main cost of Strategy B.
- **Benefit:** folds can be fully demand-driven — an actor that stays local and un-evicted may never need its prefix folded; the bundle is its durable record. Saves per-actor PUTs for cold/resident actors.
- Requires the same invariants as Strategy A (ordering, idempotency, restore-complete).

Recommendation: start with **Strategy A** (correct by construction), and only add B if measurement shows resident actors' fold PUTs are worth deferring.

### 10.5 Ordering and idempotency

- **Order:** the bundle serializes segments as `(actor, txid)` ascending; the fold writes per-actor segments in that order. litestream restore applies segments in filename/txid order, so this must be preserved.
- **Idempotency:** a fold is PUT-same-key-same-bytes. Concurrent or duplicate folds are harmless; two nodes folding the same bundle converge.
- **Restore-complete invariant:** actor A's prefix is safe to restore only when it holds a **contiguous** chain through the newest durable cut for A (litestream tolerates no gap in an L0 chain — a missing segment forces a full snapshot, or a failed restore). The watermark plus bundle retention guarantees that any segment not yet folded is still present in a retained bundle.

### 10.6 Retention / GC boundary

- A bundle is deletable only when, for **every** actor it covers, `folded_watermark(actor) ≥ bundle.max_txid(actor)` **and** the folded chain has been compacted past that point.
- This is why folds must precede GC: deleting a bundle whose segments were never folded loses acked data (the "dirty tails" rule — celld refuses to evict/restore until the tail is folded).

### 10.7 Epoch fencing

- Bundle headers carry `epoch`; folded objects are scoped under `actors/<pid>/e<epoch>/` (matching the durable-state proposal's prefix scheme).
- The materializer refuses a segment whose epoch is not the current owner's — a fenced writer's segments must never be materialized into a live chain.

### 10.8 Failure semantics

- A failed fold PUT retries; the segment stays in the bundle (retention covers it) and the actor's watermark does not advance.
- A failed bundle PUT is the ack failure — waiters get the store's failure path (queued-vs-stall budget, per Section 6), and the committer falls back to the per-actor prefix upload for that flush.
- On crash between bundle PUT and fold, the fold simply re-runs from the retained bundle on the next drain/takeover — the bundle is the durable, self-describing record of exactly what to fold.

### 10.9 Fold summary

| Concern | Answer |
|---|---|
| What is folded | The already-encoded L0 LTX segment bytes from the bundle |
| Where it goes | The actor's per-actor S3 prefix, at litestream's exact object keys |
| What is NOT touched | The local SQLite file / WAL (data is already committed) |
| Recommended mechanism | litestream's own `Replica.Sync` as the fold (Strategy A) |
| When | Periodic drain, eviction, and before takeover-restore |
| Ordering | Per-actor txid ascending, as the bundle serialized it |
| Idempotency | PUT same-key-same-bytes; concurrent folds converge |
| GC boundary | Folded watermark ≥ bundle max txid AND compacted, per actor |
| Fencing | Segments scoped by epoch; refuse stale-epoch folds |
| Restore | Prefix is restore-complete only with a contiguous folded chain |