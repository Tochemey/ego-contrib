# Offset Store (Memory)

## Overview

This module keeps [eGo](https://github.com/Tochemey/ego) projection offsets in memory.
It implements `github.com/tochemey/ego/v4/offsetstore.OffsetStore` on top of `github.com/hashicorp/go-memdb`.

A projection reads the events of each shard in order and records how far it got.
That mark is the offset, and keeping it per projection and per shard is what lets a projection resume instead of starting over.

Nothing survives the process, so a restart makes every projection replay from the beginning.
This store is meant for unit tests, examples and prototypes, not for production.

## Schema

None. The store creates its own in-memory tables on `Connect`, so there is nothing to provision.

## Installation

```bash
go get github.com/tochemey/ego-contrib/offsetstore/memory@vX.Y.Z
```

## HowTo

### Create the store

The constructor takes no argument:

```go
store := memory.NewOffsetStore()

if err := store.Connect(ctx); err != nil {
    return err
}
defer store.Disconnect(ctx)
```

`Disconnect` clears every offset it holds. Set `KeepRecordsAfterDisconnect` when a test reconnects the same store and expects its offsets to still be there:

```go
store := memory.NewOffsetStore()
store.KeepRecordsAfterDisconnect = true
```

### Plug the store into eGo

Pass the store to `ego.WithOffsetStore`, alongside the events store the projections read from:

```go
config := ego.NewConfig(eventsStore, ego.WithOffsetStore(store))

actorSystem, err := goakt.NewActorSystem("accounts", config.GoaktOptions()...)
if err != nil {
    return err
}

engine, err := ego.NewEngine(actorSystem, config)
```

Swapping this store for the PostgreSQL one is a one-line change, since both satisfy the same interface.

### Write and read an offset directly

`WriteOffset` records the position reached by a projection on one shard:

```go
err := store.WriteOffset(ctx, &egopb.Offset{
    ProjectionName: "accounts-projection",
    ShardNumber:    3,
    Value:          1712345678901,
    Timestamp:      time.Now().UnixMilli(),
})
```

`GetCurrentOffset` returns it, or `nil` when the projection never wrote it:

```go
offset, err := store.GetCurrentOffset(ctx, &egopb.ProjectionId{
    ProjectionName: "accounts-projection",
    ShardNumber:    3,
})
if offset == nil {
    // the projection has not started on this shard
}
```

### Replay a projection from the start

`ResetOffset` sets one value on every shard of a projection, leaving other projections untouched.
Passing `0` makes the projection reprocess its whole journal on the next run:

```go
err := store.ResetOffset(ctx, "accounts-projection", 0)
```

## Testing

`go test ./...` runs the suite. No Docker and no database are needed.
From the repository root, `make test/offsetstore/memory` runs the same suite.
