# eGo Contrib

[![build](https://img.shields.io/github/actions/workflow/status/Tochemey/ego-contrib/build.yml?branch=main)](https://github.com/Tochemey/ego-contrib/actions/workflows/build.yml)
[![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/tochemey/ego-contrib?filename=eventstore%2Fpostgres%2Fgo.mod)](https://go.dev/doc/install)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](./LICENSE)

Storage backends for [eGo](https://github.com/Tochemey/ego).
Each backend is a standalone Go module that implements one of eGo's persistence interfaces: durable state stores, event stores, offset stores and snapshot stores.
All modules target eGo v4 and Go 1.26.

## Modules

### Durable state stores

These modules implement `persistence.StateStore`. A durable state store keeps the latest state of each entity.

#### Memory

In-memory store backed by a `sync.Map`. Records are dropped on `Disconnect`. Use it in tests and prototypes.

- Install: `go get github.com/tochemey/ego-contrib/durablestore/memory`
- Documentation: [durablestore/memory/README.md](./durablestore/memory/README.md)

#### PostgreSQL

Stores each entity's state in the `states_store` table as protobuf bytes plus the message manifest.
The store can build its own `pgxpool.Pool` from its configuration or run on a pool you provide.

- Install: `go get github.com/tochemey/ego-contrib/durablestore/postgres`
- Schema: [durablestore_postgres.sql](./durablestore/postgres/resources/durablestore_postgres.sql)
- Documentation: [durablestore/postgres/README.md](./durablestore/postgres/README.md)

#### DynamoDB

Stores each entity's state as one item keyed by `PersistenceID`. You provide the table name and a DynamoDB client; the table must exist before the store is used.

- Install: `go get github.com/tochemey/ego-contrib/durablestore/dynamodb`
- Documentation: [durablestore/dynamodb/README.md](./durablestore/dynamodb/README.md)

#### Cassandra

Stores each entity's state in the `states_store` table of a keyspace you choose, with configurable consistency.

- Install: `go get github.com/tochemey/ego-contrib/durablestore/cassandra`
- Schema: [states_store.sql](./durablestore/cassandra/resources/states_store.sql)
- Documentation: [durablestore/cassandra/README.md](./durablestore/cassandra/README.md)

### Event stores

These modules implement `persistence.EventsStore`. An event store is the journal of event-sourced entities.

#### Memory

In-memory journal backed by `hashicorp/go-memdb`. Records are dropped on `Disconnect` unless `KeepRecordsAfterDisconnect` is set. Use it in tests and prototypes.

- Install: `go get github.com/tochemey/ego-contrib/eventstore/memory`
- Documentation: [eventstore/memory/README.md](./eventstore/memory/README.md)

#### PostgreSQL

Stores events in the `events_store` table, writes them in batches inside a transaction, and answers eGo's shard queries with a single grouped query.
The store can build its own `pgxpool.Pool` from its configuration or run on a pool you provide.

- Install: `go get github.com/tochemey/ego-contrib/eventstore/postgres`
- Schema: [eventstore_postgres.sql](./eventstore/postgres/resources/eventstore_postgres.sql)
- Documentation: [eventstore/postgres/README.md](./eventstore/postgres/README.md)

### Offset stores

These modules implement `offsetstore.OffsetStore`. An offset store records how far each projection has read on each shard.

#### Memory

In-memory store backed by `hashicorp/go-memdb`. Records are dropped on `Disconnect` unless `KeepRecordsAfterDisconnect` is set. Use it in tests and prototypes.

- Install: `go get github.com/tochemey/ego-contrib/offsetstore/memory`
- Documentation: [offsetstore/memory/README.md](./offsetstore/memory/README.md)

#### PostgreSQL

Stores one row per projection and shard in the `offsets_store` table.
The store can build its own `pgxpool.Pool` from its configuration or run on a pool you provide.

- Install: `go get github.com/tochemey/ego-contrib/offsetstore/postgres`
- Schema: [offsetstore_postgres.sql](./offsetstore/postgres/resources/offsetstore_postgres.sql)
- Documentation: [offsetstore/postgres/README.md](./offsetstore/postgres/README.md)

### Snapshot stores

These modules implement `persistence.SnapshotStore`. A snapshot store keeps point-in-time states of event-sourced entities so that recovery does not replay the whole journal.

#### PostgreSQL

Stores snapshots in the `snapshots_store` table keyed by persistence id and sequence number.
The store can build its own `pgxpool.Pool` from its configuration or run on a pool you provide.

- Install: `go get github.com/tochemey/ego-contrib/snapshotstore/postgres`
- Schema: [snapshotstore_postgres.sql](./snapshotstore/postgres/resources/snapshotstore_postgres.sql)
- Documentation: [snapshotstore/postgres/README.md](./snapshotstore/postgres/README.md)

## Usage

1. Install the module you need with `go get`.
2. Apply its schema or provision the backing service. Schemas live in the module's `resources/` directory.
3. Create the store and pass it to the eGo engine. Each module README has a complete example.

The PostgreSQL stores share one design: `New<Store>(config)` builds and owns a connection pool, `New<Store>WithPool(pool)` runs on a pool the caller owns and never closes it.
One `pgxpool.Pool` can therefore back the event, snapshot, durable state and offset stores of an application.
See the "Bringing your own connection pool" section of each PostgreSQL README.

## Versioning

The repository follows [Semantic Versioning](https://semver.org) and [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/).
All modules are released together under the same version. Pushing a root tag `vX.Y.Z` creates a `<module>/vX.Y.Z` tag for every module on the same commit.
Pin a module to a release with the module tag, for example:

```bash
go get github.com/tochemey/ego-contrib/eventstore/postgres@v0.1.0
```

## Repository layout

- `durablestore/`, `eventstore/`, `offsetstore/`, `snapshotstore/`: one directory per backend, each an independent Go module
- `go.work`: workspace listing every module, used for local development and by the IDE
- `Makefile`, `Dockerfile`: lint and test tasks, on the host or inside a Docker image with the CI toolchain
- `.github/workflows/`: lint and test pipelines, and the release pipeline that tags every module
- `contributing.md`, `code_of_conduct.md`: contribution guidelines

## Development

### Workspace

The root `go.work` puts all modules in one workspace, so the IDE resolves imports across modules and a single command from the root covers all of them:

```bash
go build github.com/tochemey/ego-contrib/...
go vet github.com/tochemey/ego-contrib/...
```

Use the import-path pattern. `./...` is refused at the root because the root directory is not a module.
Commit `go.work.sum` together with `go.work`.

### Lint and test

The `Makefile` runs each module on its own with its vendored dependencies:

```bash
make lint                       # every module
make test                       # every module, Docker must be running
make test/eventstore/postgres   # one module
make help                       # every target
```

The Makefile and the CI pipelines set `GOWORK=off`, because workspace mode does not allow per-module vendoring.
Set it yourself when running `go mod vendor` or `go test -mod=vendor` by hand inside a module.

The integration suites start their databases with Testcontainers-Go, so Docker must be running.

### Docker

`make docker-lint`, `make docker-test` and `make docker-run TARGET=test/eventstore/postgres` run the same tasks inside the image built from the `Dockerfile`, which carries the CI toolchain.
The host Docker socket is shared with the container so the suites can start their databases.
On Docker Desktop, pass `TESTCONTAINERS_HOST_OVERRIDE=host.docker.internal` when the databases are not reachable from inside the container.

### Adding a module

1. Create the module in its own directory with its own `go.mod`, `.golangci.yml` and `README.md`.
2. Register it in the `use` block of `go.work`, in the `MODULES` variable of the `Makefile`, and in the module matrices of the workflows under `.github/workflows/`, including the release workflow.

## Contributing

Read [code_of_conduct.md](./code_of_conduct.md) and [contributing.md](./contributing.md).
Open an issue before starting a large change. Run `make lint` and `make test` before opening a pull request.
