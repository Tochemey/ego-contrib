# Durable State Store (SlateDB)

## Overview

This module persists [eGo](https://github.com/Tochemey/ego) durable actor state to [SlateDB](https://slatedb.io) — an embedded LSM store whose WAL, manifest, and SSTs all live in object storage. It implements `github.com/tochemey/ego/v4/persistence.StateStore` via the official Go binding `[slatedb.io/slatedb-go/uniffi](https://pkg.go.dev/slatedb.io/slatedb-go/uniffi)`.

Because SlateDB is embedded and its durability is intrinsic to the object store, there is no database server to operate and no separate replication to configure. The same store runs fully embedded (memory:///) or against S3 / GCS / Azure Blob / MinIO.

## Features

- Complete StateStore implementation: Connect, Disconnect, Ping, WriteState, GetLatestState
- Upsert semantics keyed on persistence_id
- Two runtime modes from one Config:
  - Embedded / local: ObjectStoreURL: "memory:///" — zero network, zero AWS
  - Object-store-backed: S3 / GCS / Azure / MinIO via ObjectStoreR`esolve` or environment configuration
- Configurable durability: `DurabilityNone`, `DurabilityDurable` (default, awaits flush to object storage), `DurabilityFlush` (background flusher)



## Runtime Requirements

The `slatedb-go/uniffi` binding links against the `slatedb_uniffi` Rust shared library. To build and run this module you need:

- Go 1.25 or newer
- `CGO_ENABLED=1`
- a working C toolchain
- the `slatedb_uniffi` shared library on your platform loader path (`LD_LIBRARY_PATH` / `DYLD_LIBRARY_PATH`)

Build the library from the [SlateDB repository](https://github.com/slatedb/slatedb) at the matching version:

```bash
git clone --depth 1 --branch v0.16.0 https://github.com/slatedb/slatedb.git
cd slatedb
cargo build --package slatedb-uniffi
export DYLD_LIBRARY_PATH="$(pwd)/target/debug:${DYLD_LIBRARY_PATH}"   # macOS
export LD_LIBRARY_PATH="$(pwd)/target/debug:${LD_LIBRARY_PATH}"       # linux
```



## Installation

```bash
go get github.com/tochemey/ego-contrib/durablestore/slatedb
```



## Quickstart

```go
package main

import (
	"context"
	"log"

	slatedbstore "github.com/tochemey/ego-contrib/durablestore/slatedb"
)

func main() {
	ctx := context.Background()

	// Embedded, no network: use "memory:///" as the object store URL.
	// For durable object-store mode, point at S3/GCS/Azure, e.g.
	//   ObjectStoreURL: "s3://my-bucket/ego/durable"
	store := slatedbstore.NewDurableStore(&slatedbstore.Config{
		ObjectStoreURL: "memory:///",
		DbPath:         "ego/durable",
	})

	if err := store.Connect(ctx); err != nil {
		log.Fatal(err)
	}
	defer store.Disconnect(ctx)

	// WriteState / GetLatestState use *egopb.DurableState
	// (see the ego docs for building durable state).
	_ = store
}
```



## Configuration


| Field               | Description                                                                       |
| ------------------- | --------------------------------------------------------------------------------- |
| `ObjectStoreURL`    | SlateDB object store URL, e.g. `memory:///`, `s3://bucket/ego/durable`            |
| `EnvFile`           | Optional file whose env config is used to build the object store                  |
| `DbPath`            | Logical database path within the object store (required; keep distinct per store) |
| `Settings`          | SlateDB dotted settings -> JSON literal values (e.g. `flush_interval`)            |
| `WalObjectStoreURL` | Optional separate lower-latency object store for the WAL                          |
| `Seed`              | Optional seed passed to the DbBuilder                                             |
| `Durability`        | `DurabilityDurable` (default) / `DurabilityNone` / `DurabilityFlush`              |
| `FlushInterval`     | Interval of the background flusher when `DurabilityFlush` (default 1s)            |




## Durability notes

- `DurabilityDurable` (default) awaits the write being flushed to object storage before `WriteState` returns — smallest RPO, one flush per write.
- `DurabilityNone` returns after the in-memory write; data is lost on a crash before a flush. Only meaningful for the embedded `memory:///` mode.
- `DurabilityFlush` commits locally and flushes on a background interval plus a final flush on `Disconnect`; RPO is bounded by `FlushInterval`.
- With `memory:///`, the object store is in-process only and does not survive a process restart. Use a cloud object store for cross-process durability.



## References

- [SlateDB](https://slatedb.io)
- [slatedb-go Go binding](https://pkg.go.dev/slatedb.io/slatedb-go/uniffi)

