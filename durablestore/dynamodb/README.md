# Durable State Store (DynamoDB)

## Overview

This module persists the durable state of [eGo](https://github.com/Tochemey/ego) entities in Amazon DynamoDB.
It implements `github.com/tochemey/ego/v4/persistence.StateStore` on top of the AWS SDK for Go v2.

A durable state entity keeps no journal. Only its latest state is stored, as protobuf bytes together with the full name of its message, so it can be unmarshalled back into the right type when the entity restarts.
Each write is a `PutItem` on the persistence id, so the table holds exactly one item per entity and the last write wins.

The store holds no connection. `Connect`, `Disconnect` and `Ping` do nothing, because the DynamoDB client is stateless and manages its own HTTP transport.

## Schema

Create the table before starting your application. It needs a single attribute in its key schema:

| Attribute | Type | Role |
|---|---|---|
| `PersistenceID` | String | Partition key |
| `VersionNumber` | Number | Version of the state, written by eGo |
| `StatePayload` | Binary | Serialized protobuf bytes |
| `StateManifest` | String | Protobuf message name used to rebuild the state |
| `Timestamp` | Number | Unix epoch milliseconds |
| `ShardNumber` | Number | Shard the entity belongs to |

Only the partition key has to be declared. DynamoDB is schemaless for the other attributes, and the store writes them on every item.

```bash
aws dynamodb create-table \
  --table-name states_store \
  --attribute-definitions AttributeName=PersistenceID,AttributeType=S \
  --key-schema AttributeName=PersistenceID,KeyType=HASH \
  --billing-mode PAY_PER_REQUEST
```

On-demand billing suits an unpredictable write rate. Switch to provisioned capacity when your throughput is steady and known.

## Installation

```bash
go get github.com/tochemey/ego-contrib/durablestore/dynamodb@vX.Y.Z
```

## HowTo

### Create the store

The store takes a table name and a DynamoDB client you build and own:

This module's package is named `dynamodb`, like the AWS SDK one, so alias it on import:

```go
import (
    "github.com/aws/aws-sdk-go-v2/config"
    "github.com/aws/aws-sdk-go-v2/service/dynamodb"
    ddbstore "github.com/tochemey/ego-contrib/durablestore/dynamodb"
)

cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion("us-east-1"))
if err != nil {
    return err
}

client := dynamodb.NewFromConfig(cfg)
store := ddbstore.NewDurableStore("states_store", client)
```

Credentials, region, retries and endpoints come from the AWS configuration, so the store adds no settings of its own.
Point the client at DynamoDB Local by setting a base endpoint on it, which is what the test helper does.

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

`WriteState` upserts the item of the entity:

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

The read is a `GetItem` on the partition key, so it is eventually consistent by default.
An entity that was just written on another node may therefore read a slightly stale state.

### Unmarshalling on read

The store resolves the state through `protoregistry.GlobalTypes` using the manifest recorded next to the payload.
Import the generated packages of your state messages in the binary that reads them, otherwise the lookup fails.

## Testing

`go test ./...` runs the suite. Docker must be running, since the tests start DynamoDB Local with Testcontainers-Go and create the table themselves.
From the repository root, `make test/durablestore/dynamodb` runs the same suite.
