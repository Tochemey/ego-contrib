# Durable State Store (Cassandra)

## Overview

This module persists the durable state of [eGo](https://github.com/Tochemey/ego) entities in Apache Cassandra.
It implements `github.com/tochemey/ego/v4/persistence.StateStore` on top of `github.com/apache/cassandra-gocql-driver/v2`.

A durable state entity keeps no journal. Only its latest state is stored, as protobuf bytes together with the full name of its message, so it can be unmarshalled back into the right type when the entity restarts.
Each write is a plain `INSERT`, which Cassandra applies as an upsert on the partition key, so the table holds one row per entity.

## Schema

Create the keyspace and the table before starting your application:

```bash
cqlsh -e "CREATE KEYSPACE IF NOT EXISTS ego WITH replication = {'class': 'SimpleStrategy', 'replication_factor': 1};"
cqlsh -k ego -f resources/states_store.sql
```

The single-node replication above suits local work. Use `NetworkTopologyStrategy` with a replication factor per datacenter in a real cluster.

The DDL creates the `states_store` table:

```sql
CREATE TABLE IF NOT EXISTS states_store (
    version_number  bigint,
    persistence_id  text,
    state_payload   blob,
    state_manifest  text,
    timestamp       bigint,
    shard_number    bigint,
    PRIMARY KEY (persistence_id)
);
```

The persistence id is the partition key, so a state is read and written by a single-partition query.
`version_number` is incremented by eGo on every state change and `state_manifest` holds the protobuf message name used to rebuild the state.

## Installation

```bash
go get github.com/tochemey/ego-contrib/durablestore/cassandra@vX.Y.Z
```

## HowTo

### Create the store

The store takes the cluster address, the keyspace and the consistency to use:

```go
store := cassandra.NewDurableStore(&cassandra.Config{
    Cluster:     "127.0.0.1",
    Keyspace:    "ego",
    Consistency: gocql.Quorum,
})

if err := store.Connect(ctx); err != nil {
    return err
}
defer store.Disconnect(ctx)
```

`Connect` opens the session and `Disconnect` closes it. Both are safe to call more than once.

The consistency applies to every read and write the store issues.
`gocql.Quorum` is the usual choice, since it keeps reads and writes consistent with each other on a replicated keyspace.
`gocql.One` is faster but can return a state that a recent write has already replaced.

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

### Write and read a state directly

`WriteState` upserts the row of the entity:

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

### Unmarshalling on read

The store resolves the state through `protoregistry.GlobalTypes` using the manifest recorded next to the payload.
Import the generated packages of your state messages in the binary that reads them, otherwise the lookup fails.

## Testing

`go test ./...` runs the suite. Docker must be running, since the tests start Cassandra with Testcontainers-Go and create the keyspace themselves.
Starting the container takes a while, so expect the suite to run for about a minute.

From the repository root, `make test/durablestore/cassandra` runs the same suite.
