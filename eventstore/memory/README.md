# Events Store (Memory)

## Overview

This module keeps [eGo](https://github.com/Tochemey/ego) event journals in memory.
It implements `github.com/tochemey/ego/v4/persistence.EventsStore` on top of `github.com/hashicorp/go-memdb`, which gives it indexed lookups and transactional reads without a database.

Nothing survives the process. This store is meant for unit tests, examples and prototypes, not for production.

## Schema

None. The store creates its own in-memory tables on `Connect`, so there is nothing to provision.

## Installation

```bash
go get github.com/tochemey/ego-contrib/eventstore/memory@vX.Y.Z
```

## HowTo

### Create the store

The constructor takes no argument:

```go
store := memory.NewEventsStore()

if err := store.Connect(ctx); err != nil {
    return err
}
defer store.Disconnect(ctx)
```

`Disconnect` clears every event it holds. Set `KeepRecordsAfterDisconnect` when a test reconnects the same store and expects its events to still be there:

```go
store := memory.NewEventsStore()
store.KeepRecordsAfterDisconnect = true
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

Swapping this store for the PostgreSQL one is a one-line change, since both satisfy the same interface.
A test can therefore exercise the same entities as production without a database.

### Write and read events directly

`WriteEvents` appends a batch:

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

`ReplayEvents` returns the events of one entity between two sequence numbers, both included, and `GetLatestEvent` returns the last one, or `nil` when the entity has no event:

```go
events, err := store.ReplayEvents(ctx, "account-42", 1, 100, 500)
latest, err := store.GetLatestEvent(ctx, "account-42")
```

`DeleteEvents` removes the events of an entity up to a sequence number, included:

```go
err := store.DeleteEvents(ctx, "account-42", 50)
```

### Read a shard for projections

`ShardOffsets` maps every shard that holds events to the timestamp of its most recent event, which is how eGo finds the shards a projection still has to read:

```go
offsets, err := store.ShardOffsets(ctx) // for instance map[uint64]int64{3: 1712345678901}
```

`GetShardEvents` then reads the next events of a shard after an offset, and returns the offset to pass on the next call:

```go
events, nextOffset, err := store.GetShardEvents(ctx, 3, offset, 100)
```

### Unmarshalling on replay

The store resolves each event through `protoregistry.GlobalTypes` using the manifest recorded next to the payload.
Import the generated packages of your event messages, otherwise the lookup fails.

## Testing

`go test ./...` runs the suite. No Docker and no database are needed.
From the repository root, `make test/eventstore/memory` runs the same suite.
