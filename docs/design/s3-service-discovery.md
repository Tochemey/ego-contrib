# Design Proposal: S3 Service Discovery (ClusterProvider)

- **Status:** Proposal (draft for review)
- **Module:** `discovery/s3`
- **Scope:** Cluster peer discovery only, implementing eGo's `ClusterProvider` interface. Storage backends (durable state, event, offset, snapshot) are **out of scope**.
- **Maintainer:** eGo Contrib community

## 1. Motivation

eGo cluster mode needs a `ClusterProvider` to discover peer nodes. The providers shipped today with GoAkt (consul, etcd, kubernetes, nats, dnssd, mdns, static, selfmanaged) each carry an operational cost:

- Consul / etcd / NATS require a dedicated service to run, watch, and back up.
- Kubernetes discovery requires cluster-internal API access and a service account.
- mDNS / selfmanaged (UDP broadcast) only work on a single broadcast domain.

For teams already on AWS — or running on serverless / edge / VM fleets where none of the above is guaranteed — an S3 bucket is a natural, already-available coordination point. It requires no new service to operate, works across VPCs and regions (unlike multicast), and tolerates the `host:port` seed/steady-state discovery pattern eGo needs.

The core design decision in this proposal is to use a **shared S3 bucket as an append-only peer registry**: nodes register themselves by writing an object, discover peers by listing the bucket prefix, keep their registration alive with a heartbeat, and are ignored once their object goes stale.

## 2. Goals / Non-Goals

### Goals
- Implement the eGo `ClusterProvider` interface (`ID`, `Start`, `DiscoverPeers`, `Stop`).
- Register a node by `PutObject` under a per-actor-system key prefix.
- Discover peers with a single `ListObjectsV2` call per refresh cycle.
- Graceful deregistration on `Stop` via `DeleteObject`.
- **Crash-safe staleness**: a node that dies without deregistering is filtered out once its object's `LastModified` exceeds a configurable TTL (heartbeat keeps it fresh while alive).
- Support S3-compatible endpoints (MinIO, LocalStack) so the module can be tested and used off AWS.
- Exclude self from the returned peer set (mirrors the Consul provider).

### Non-Goals
- **Membership / consensus**: this is not a lease, fencing, or leader-election protocol. It only supplies the peer address set; eGo's gossip layer (Memberlist) owns real-time membership and failure detection.
- **Split-brain resolution**.
- **Low-latency membership change notification**: S3 discovery refresh is on the order of seconds, adequate for bootstrap and drift correction, not for sub-second failover. Nodes should also be reached by gossip once joined.
- Multi-region active/active registration: recommend one registry region per cluster (see §12).
- Durable state / event / offset / snapshot persistence.

## 3. Background

### 3.1 eGo `ClusterProvider`
The store must satisfy (see `cluster_provider.go` in `github.com/tochemey/ego/v4`):

```go
type ClusterProvider interface {
    ID() string
    Start(ctx context.Context) error          // called once, ctx cancelled on shutdown
    DiscoverPeers(ctx context.Context) ([]string, error)
    Stop(ctx context.Context) error
}
```

- `DiscoverPeers` returns `"host:port"` entries where the port is the **discovery port** (e.g. `"192.168.1.10:7946"`); use `net.JoinHostPort` for IPv6.
- eGo wraps the provider in a `clusterProviderAdapter` that bridges it to GoAkt's `discovery.Provider` (`Initialize` → `Start`, `DiscoverPeers` → `DiscoverPeers`, `Close` → `Stop`). The adapter cancels the context passed to `Start` when the engine shuts down, which gives us a clean signal to stop the heartbeat.
- The provider is wired in via `ego.WithCluster(provider, partitionCount, quorum, host, remotingPort, discoveryPort, peersPort)`.

### 3.2 Why S3 works for this
- **Uniqueness & upsert semantics**: each node owns its object key, so registration is a conflict-free `PutObject` overwrite.
- **Single-round-trip discovery**: `ListObjectsV2` on a prefix returns all registrations in one call, including `LastModified`, which we can use directly for staleness — no per-peer `GetObject`/`HeadObject` needed.
- **Strong read-after-write consistency**: as of December 2020 AWS S3 is strongly consistent for `PUT`, `GET`, and `LIST`, so a just-written registration is visible immediately. S3-compatible stores (single-node MinIO, LocalStack) are strongly consistent too; genuinely eventually-consistent stores are a caveat (§13).
- **No extra service**: the bucket already exists in most AWS workloads; only the object-store permissions need to be granted.

## 4. Design Overview

```
        node A                                node B
          │ Start()                             │ Start()
          │ PutObject(self)                     │ PutObject(self)
          │ heartbeat ticker ── PutObject ──▶   │ heartbeat ticker ── PutObject ──▶
          │                                     │
          │ DiscoverPeers()                     │ DiscoverPeers()
          │   ListObjectsV2(prefix)             │   ListObjectsV2(prefix)
          │   ──▶ peers A' + B'                 │   ──▶ peers A' + B'
          │ Stop()                              │ Stop()
          │ DeleteObject(self)                  │ DeleteObject(self)
          ▼                                     ▼

                    s3://<bucket>/<prefix>/<actorSystemName>/nodes/
                        <nodeKeyA>  (body: {host, ports, joinedAt})
                        <nodeKeyB>
```

### 4.1 Registry layout
```
s3://<bucket>/<Prefix>/<ActorSystemName>/nodes/<nodeKey>
```

- `<Prefix>` defaults to `ego/discovery` and lets unrelated deployments share one bucket.
- `<ActorSystemName>` partitions registrations so two actor systems sharing a bucket never see each other.
- `<nodeKey>` is a **deterministic** encoding of the node address so peers can recover `host:port` from the key alone, without a `GetObject` per peer. Colons in the key are legal in S3 but are replaced to stay friendly to S3-compatible stores:

  ```go
  // e.g. "192.168.1.10:7946"  -> "192.168.1.10_7946"
  //      "[::1]:7946"          -> "___1_7946"  (bracket/dot/colon all normalized)
  func nodeKey(host string, discoveryPort int) string {
      k := net.JoinHostPort(host, strconv.Itoa(discoveryPort))
      return strings.NewReplacer("[", "", "]", "", ":", "_", ".", "_").Replace(k)
  }
  ```

  Using the address (rather than a UUID) makes registrations idempotent across restarts: a restarting node overwrites its own key.

### 4.2 Object body
The body is a small JSON document (content-type `application/json`) describing the node; peers only need the key today, but the body keeps the registry human-inspectable and future-proof (e.g. roles, remoting/gossip ports):

```json
{
  "actorSystem": "banking",
  "nodeKey":     "192.168.1.10_7946",
  "host":        "192.168.1.10",
  "discoveryPort": 7946,
  "remotingPort":  3552,
  "peersPort":     3553,
  "joinedAt":      "2026-09-06T10:00:00Z"
}
```

### 4.3 Liveness: heartbeat + staleness
S3 has no native TTL, so liveness is enforced by the consumers:

- While running, the node re-`PutObject`s its registration every `HeartbeatInterval` (default `10s`), bumping the object's `LastModified`.
- `DiscoverPeers` treats an object as alive only if `now - LastModified <= StaleAfter` (default `3 × HeartbeatInterval = 30s`).
- A node that crashes silently stops heartbeating and is naturally filtered out after `StaleAfter` — no sweeper or leader needed. A bucket lifecycle rule can additionally purge old keys (§12).

The heartbeat goroutine runs until the `Start` context is cancelled (which the eGo adapter guarantees on shutdown), so there is no leaked ticker.

## 5. Lifecycle & Concurrency

```go
type Discovery struct {
    mu          sync.RWMutex
    cfg         *Config
    client      *s3.Client
    initialized atomic.Bool
    registered  atomic.Bool
    done        chan struct{}   // closed on Stop to halt heartbeat
    wg          sync.WaitGroup  // heartbeat goroutine
    selfKey     string
}

var _ ego.ClusterProvider = (*Discovery)(nil)
```

- `NewDiscovery(cfg *Config) *Discovery` — validates lazily in `Start`; returns a zero-config-defaulted instance like the GoAkt providers (`NewDiscovery(nil)`).
- `Start(ctx) error`:
  1. `cfg.Sanitize()` then `cfg.Validate()`.
  2. Build the S3 client (aws-sdk-go-v2) using the config or the default credential chain.
  3. Compute `selfKey`, `PutObject` the registration.
  4. Launch the heartbeat goroutine keyed to `ctx.Done()` and `done`.
- `DiscoverPeers(ctx) ([]string, error)` — `ListObjectsV2(prefix)`; map each alive key back to `host:discoveryPort` via the inverse of `nodeKey`; **skip self**; return `[]string`.
- `Stop(ctx) error` — close `done`, wait for the heartbeat goroutine, `DeleteObject(selfKey)`.
- Idempotency guards mirror the Consul provider: calling `Start` twice returns `ErrAlreadyInitialized`/`ErrAlreadyRegistered`; `DiscoverPeers` before `Start` returns an error.

### 5.1 S3 client seam
To keep unit tests hermetic (no container, no network), `Discovery` depends on a tiny internal interface rather than the concrete SDK client:

```go
type objectStore interface {
    PutObject(ctx context.Context, key string, body []byte) error
    ListObjects(ctx context.Context, prefix string) ([]ObjectMeta, error) // key + LastModified
    DeleteObject(ctx context.Context, key string) error
}
```

The production adapter wraps `aws-sdk-go-v2/service/s3`; tests use an in-memory fake. The integration suite then exercises the real adapter against MinIO.

## 6. Configuration

```go
type Config struct {
    Bucket           string        // required; registry bucket (must exist)
    Region           string        // optional; default = SDK chain
    Endpoint         string        // optional; S3-compatible endpoint (MinIO/LocalStack)
    UsePathStyle     bool          // required for MinIO/LocalStack
    AccessKey        string        // optional; default = SDK credential chain / IAM role
    SecretKey        string        // optional
    SessionToken     string        // optional
    ActorSystemName  string        // required; partitions the registry
    Prefix           string        // optional; key prefix (default "ego/discovery")
    Host             string        // required; this node's advertised host
    DiscoveryPort    int           // required; this node's discovery/gossip port
    HeartbeatInterval time.Duration // default 10s
    StaleAfter       time.Duration  // default 3 × HeartbeatInterval
    RequestTimeout   time.Duration  // default 5s, per S3 call
}
```

- `Host` and `DiscoveryPort` mirror the values passed to `ego.WithCluster(..., host, ..., discoveryPort, ...)`; callers construct `Config` from the same values.
- `Sanitize()` fills defaults; `Validate()` enforces `Bucket`, `ActorSystemName`, `Host`, `DiscoveryPort > 0`, and `StaleAfter >= HeartbeatInterval`.
- The bucket is **not auto-created** — provisioning and lifecycle policy stay with the operator (avoids needing `s3:CreateBucket`, which is disallowed in many IAM policies).

## 7. Implementation Sketch (Go)

```go
// discovery.go
package s3

func (d *Discovery) ID() string { return "s3" }

func (d *Discovery) Start(ctx context.Context) error {
    d.mu.Lock(); defer d.mu.Unlock()
    if d.initialized.Load() { return ErrAlreadyInitialized }
    d.cfg.Sanitize()
    if err := d.cfg.Validate(); err != nil { return fmt.Errorf("s3 discovery config invalid: %w", err) }

    store, err := newS3Store(d.cfg) // wraps aws-sdk-go-v2/service/s3
    if err != nil { return err }
    d.client = store
    d.selfKey = keyFor(d.cfg.Prefix, d.cfg.ActorSystemName, nodeKey(d.cfg.Host, d.cfg.DiscoveryPort))

    if err := d.client.PutObject(ctx, d.selfKey, marshalNode(d.cfg)); err != nil {
        return fmt.Errorf("failed to register with s3: %w", err)
    }

    d.initialized.Store(true)
    d.registered.Store(true)
    d.wg.Add(1)
    go d.heartbeat(ctx)
    return nil
}

func (d *Discovery) heartbeat(ctx context.Context) {
    defer d.wg.Done()
    t := time.NewTicker(d.cfg.HeartbeatInterval)
    defer t.Stop()
    for {
        select {
        case <-ctx.Done():
            return
        case <-d.done:
            return
        case <-t.C:
            _ = d.client.PutObject(ctx, d.selfKey, marshalNode(d.cfg)) // bump LastModified
        }
    }
}

func (d *Discovery) DiscoverPeers(ctx context.Context) ([]string, error) {
    d.mu.RLock(); defer d.mu.RUnlock()
    if !d.initialized.Load() { return nil, ErrNotInitialized }

    objects, err := d.client.ListObjects(ctx, dirFor(d.cfg.Prefix, d.cfg.ActorSystemName))
    if err != nil { return nil, fmt.Errorf("failed to list peers: %w", err) }

    peers := goset.NewSet[string]()
    now := time.Now()
    for _, obj := range objects {
        if obj.Key == d.selfKey { continue }
        if now.Sub(obj.LastModified) > d.cfg.StaleAfter { continue } // crashed node
        host, port, ok := parseNodeKey(path.Base(obj.Key))
        if !ok { continue }
        peers.Add(net.JoinHostPort(host, strconv.Itoa(port)))
    }
    return peers.ToSlice(), nil
}

func (d *Discovery) Stop(ctx context.Context) error {
    d.mu.Lock(); defer d.mu.Unlock()
    if !d.registered.Load() { return nil }
    close(d.done)
    d.wg.Wait()
    _ = d.client.DeleteObject(ctx, d.selfKey)
    d.registered.Store(false)
    return nil
}
```

## 8. Module layout

```
discovery/s3/
  discovery.go    # ClusterProvider implementation
  config.go       # Config + defaults + Validate
  s3store.go      # objectStore interface + aws-sdk-go-v2 adapter
  nodekey.go      # nodeKey / parseNodeKey encoding helpers
  heartbeat.go    # background heartbeat loop (or folded into discovery.go)
  helper_test.go  # TestMain + MinIO testcontainer (repo convention)
  README.md
  Earthfile       # module build (mirrors sibling modules)
  go.mod          # module go.mod
```

The module ships as `github.com/tochemey/ego-contrib/discovery/s3`.

- Dependency: `github.com/aws/aws-sdk-go-v2/service/s3` (+ `config` for the default credential chain). SDK v2 is chosen over v1: it is the maintained SDK, supports Go 1.26, and its modular design keeps the module's `go.mod` small.
- Copyright header, LICENSE, linter config, and Earthfile mirror the existing storage modules.

## 9. Usage

```go
package main

import (
    "context"

    "github.com/tochemey/ego-contrib/discovery/s3"
    "github.com/tochemey/ego/v4"
)

func main() {
    ctx := context.Background()

    provider := s3.NewDiscovery(&s3.Config{
        Bucket:          "ego-cluster-registry",
        Region:          "eu-west-1",
        ActorSystemName: "banking",
        Host:            "192.168.1.10",
        DiscoveryPort:   7946,
    })

    // wire into the engine exactly like any other provider
    system, err := ego.NewEngine("banking",
        ego.WithCluster(provider, 256, 2, "192.168.1.10", 3552, 7946, 3553),
    )
    // ...
    _ = ctx
}
```

## 10. Error handling & resilience

- **Transient S3 failures** in `DiscoverPeers` (throttling, network blips): return the error to the caller; eGo's cluster subsystem refreshes periodically and will retry on the next cycle. No in-memory peer cache in v1 (keeps semantics simple); a last-known-peers cache is an option if callers report flapping (§13).
- **Heartbeat failures**: non-fatal, logged and retried on the next tick; a long outage just makes the node stale and, eventually, unreachable — which is the correct degraded behavior.
- **`Stop` best-effort delete**: if `DeleteObject` fails the object lingers until it goes stale; a bucket lifecycle rule bounds how long (§12).

## 11. Testing strategy

- **Unit tests (hermetic, in-memory `objectStore` fake):**
  - Config validation (missing bucket / actor system / host / port).
  - `nodeKey` / `parseNodeKey` round-trip incl. IPv6 and hostnames.
  - Registration idempotency and self-exclusion from the peer set.
  - Staleness filtering (object older than `StaleAfter` is dropped; fresh object kept).
  - Lifecycle guards (`Start` twice, `DiscoverPeers` before `Start`).
  - Interface conformance: `assert` that `*Discovery` implements `ego.ClusterProvider`.
- **Integration tests with Testcontainers-Go** (matching sibling modules) using a **MinIO** container as the S3 endpoint:
  - Two `Discovery` instances against the same bucket discover each other.
  - A third node starting later is discovered on the next cycle.
  - Graceful shutdown: `Stop` removes the object, peers no longer see it.
  - Crash simulation: stop heartbeating without deregistering; after `StaleAfter` the node disappears from peer lists.
- **Earthfile**: `+test` target mirrors sibling modules (`WITH DOCKER` running MinIO, `go test -race`).

## 12. Operational notes

- **IAM permissions** for the running node:
  ```json
  {
    "Effect": "Allow",
    "Action": ["s3:PutObject", "s3:GetObject", "s3:DeleteObject", "s3:ListBucket"],
    "Resource": ["arn:aws:s3:::ego-cluster-registry",
                 "arn:aws:s3:::ego-cluster-registry/ego/discovery/*"]
  }
  ```
  On EKS/EC2 these come from an instance role / IRSA; no static keys required.
- **Bucket lifecycle rule** to bound storage of crashed-node registrations, e.g. expire objects under `ego/discovery/` after 7 days.
- **Single registry region** per cluster; the heartbeat cadence (10s) is fine within one region. Cross-region gossip is out of scope.
- Discovery is a **seed / steady-state provider**: it refreshes the peer set on the order of seconds. Once nodes join, Memberlist gossip carries live membership; S3 staleness is a backstop, not a failure detector.
- The registry is human-inspectable (`aws s3 ls`), which is handy for debugging cluster bootstrap.

## 13. Open questions

1. **Key vs body for peer addressing**: the proposal encodes the address in the object key (zero extra reads). If richer node metadata (roles, custom ports) becomes a requirement, switch to opaque UUID keys + a `GetObject` per peer in `DiscoverPeers`. Should v1 already store roles in the body and read them?
2. **Staleness clock**: `LastModified` is server clock; a node with skewed local clock can mis-filter. Alternative: nodes write a client-clock heartbeat timestamp in object metadata (requires a `HeadObject` per peer). Which clock domain should we standardize on?
3. **Heartbeat mechanism**: `PutObject` (rewrites body + changes ETag) vs `CopyObject` onto itself (metadata-only refresh). `CopyObject` is cheaper/quieter but needs `s3:GetObject`+`s3:PutObject` anyway; confirm the preferred one.
4. **Error policy on `DiscoverPeers`**: return error on transient S3 failure (v1) vs serve last-known peers from a cache. Confirm which behavior the eGo cluster loop tolerates best.
5. **Placement**: top-level `discovery/` directory (as proposed) vs nesting under an existing tree; and whether to also expose a raw GoAkt `discovery.Provider` (in addition to `ego.ClusterProvider`) for users not going through eGo.
6. **Auto-provisioning**: require a pre-created bucket (proposed) vs optionally `CreateBucket` when `Bucket` is missing — the latter needs `s3:CreateBucket` and changes the permission story.

## 14. References

- eGo `ClusterProvider` interface: `github.com/tochemey/ego/v4` → `cluster_provider.go`.
- GoAkt discovery providers (pattern reference): `github.com/tochemey/goakt/v4/discovery` → `consul`, `static`, `selfmanaged`.
- eGo `WithCluster` option: `github.com/tochemey/ego/v4` → `option.go`.
- AWS SDK for Go v2 S3: `github.com/aws/aws-sdk-go-v2/service/s3`.
- AWS S3 strong consistency announcement (Dec 2020): https://aws.amazon.com/blogs/aws/amazon-s3-update-strong-read-after-write-consistency/