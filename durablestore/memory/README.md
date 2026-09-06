# Durable State Store (Memory)

## Overview

This module keeps the durable state of [eGo](https://github.com/Tochemey/ego) entities in memory.
It implements `github.com/tochemey/ego/v4/persistence.StateStore` on top of a `sync.Map`, so reads and writes are safe from several goroutines.

A durable state entity keeps no journal. Only its latest state is held, one entry per persistence id, and writing a state replaces the previous one.

Nothing survives the process. This store is meant for unit tests, examples and prototypes, not for production.

## Schema

None. The store holds its entries in memory, so there is nothing to provision.

## Installation

```bash
go get github.com/tochemey/ego-contrib/durablestore/memory@vX.Y.Z
```

## HowTo

### Create the store

The constructor takes no argument:

```go
store := memory.NewStateStore()

if err := store.Connect(ctx); err != nil {
    return err
}
defer store.Disconnect(ctx)
```

`Disconnect` clears every state it holds, and this store has no option to keep them.
When a test needs its states across a reconnect, keep the store connected for the whole test instead.

### Plug the store into eGo

Pass the store to `ego.WithStateStore`. Durable state deployments that host no event-sourced entity pass `nil` as the events store:

```go
config := ego.NewConfig(nil, ego.WithStateStore(store))

actorSystem, err := goakt.NewActorSystem("accounts", config.GoaktOptions()...)
if err != nil {
    return err
}

engine, err := ego.NewEngine(actorSystem, config)
```

Swapping this store for the PostgreSQL, DynamoDB or Cassandra one is a one-line change, since they all satisfy the same interface.

### Write and read a state directly

`WriteState` stores the state under its persistence id, replacing whatever was there:

```go
payload, err := anypb.New(&accountpb.AccountState{AccountId: "account-42", BalanceCents: 4200})
if err != nil {
    return err
}

err = store.WriteState(ctx, &egopb.DurableState{
    PersistenceId:  "account-42",
    VersionNumber:  2,
    ResultingState: payload,
    Timestamp:      time.Now().UnixMilli(),
    Shard:          3,
})
```

`GetLatestState` returns the stored state, or `nil` when the entity has never been written:

```go
state, err := store.GetLatestState(ctx, "account-42")
if state == nil {
    // no state recorded for this entity
}
```

Unlike the database-backed stores, this one keeps the protobuf message as it was given, so nothing is marshalled or unmarshalled and no manifest lookup takes place.

## Testing

`go test ./...` runs the suite. No Docker and no database are needed.
From the repository root, `make test/durablestore/memory` runs the same suite.
