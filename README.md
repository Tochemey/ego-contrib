# eGo Contrib

[![build](https://img.shields.io/github/actions/workflow/status/Tochemey/ego-contrib/build.yml?branch=main)](https://github.com/Tochemey/ego-contrib/actions/workflows/build.yml)
[![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/tochemey/ego-contrib?filename=eventstore%2Fpostgres%2Fgo.mod)](https://go.dev/doc/install)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](./LICENSE)

Storage backends for [eGo](https://github.com/Tochemey/ego).
Each backend is a standalone Go module that implements one of eGo's persistence interfaces: durable state stores, event stores, offset stores and snapshot stores.
All modules target eGo v4 and Go 1.26.

## Modules

Each module is a standalone Go module. Install it with `go get github.com/tochemey/ego-contrib/<path>`.
Its README has the schema, the configuration and a complete example.

- Durable state stores
  - [Memory](./durablestore/memory)
  - [PostgreSQL](./durablestore/postgres)
  - [DynamoDB](./durablestore/dynamodb)
  - [Cassandra](./durablestore/cassandra)
- Event stores
  - [Memory](./eventstore/memory)
  - [PostgreSQL](./eventstore/postgres)
- Offset stores
  - [Memory](./offsetstore/memory)
  - [PostgreSQL](./offsetstore/postgres)
- Snapshot stores
  - [PostgreSQL](./snapshotstore/postgres)

## Usage

1. Install the module you need. All modules are released together under the same version, so pin them to the same tag:
   ```bash
   go get github.com/tochemey/ego-contrib/eventstore/postgres@v0.1.0
   ```
2. Apply its schema or provision the backing service. Schemas live in the module's `resources/` directory.
3. Create the store and pass it to the eGo engine. Each module README has a complete example.

Releases follow [Semantic Versioning](https://semver.org).

## Contributing

Read [contributing.md](./contributing.md) for the repository layout, the build and test workflow, and the pull request checklist, and [code_of_conduct.md](./code_of_conduct.md) before participating.
