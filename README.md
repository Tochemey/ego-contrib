# eGo Contrib

[![build](https://img.shields.io/github/actions/workflow/status/Tochemey/ego-contrib/build.yml?branch=main)](https://github.com/Tochemey/ego-contrib/actions/workflows/build.yml)
[![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/tochemey/ego-contrib?filename=eventstore%2Fpostgres%2Fgo.mod)](https://go.dev/doc/install)
[![release](https://img.shields.io/github/v/tag/Tochemey/ego-contrib?sort=semver&filter=v*&label=release)](https://github.com/Tochemey/ego-contrib/tags)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](./LICENSE)

Storage backends for [eGo](https://github.com/Tochemey/ego).
Each backend is a standalone Go module that implements one of eGo's persistence interfaces.
All modules target eGo v4 and Go 1.26.

## Modules

- Durable state stores
  - [Memory](./durablestore/memory)
  - [PostgreSQL](./durablestore/postgres)
  - [SQLite](./durablestore/sqlite)
  - [DynamoDB](./durablestore/dynamodb)
  - [Cassandra](./durablestore/cassandra)
- Event stores
  - [Memory](./eventstore/memory)
  - [PostgreSQL](./eventstore/postgres)
  - [SQLite](./eventstore/sqlite)
- Offset stores
  - [Memory](./offsetstore/memory)
  - [PostgreSQL](./offsetstore/postgres)
  - [SQLite](./offsetstore/sqlite)
- Snapshot stores
  - [PostgreSQL](./snapshotstore/postgres)
  - [SQLite](./snapshotstore/sqlite)

## Usage

All modules are released together under the same version, following [Semantic Versioning](https://semver.org).
Replace `vX.Y.Z` with the release you want:

```bash
go get github.com/tochemey/ego-contrib/durablestore/memory@vX.Y.Z
go get github.com/tochemey/ego-contrib/durablestore/postgres@vX.Y.Z
go get github.com/tochemey/ego-contrib/durablestore/sqlite@vX.Y.Z
go get github.com/tochemey/ego-contrib/durablestore/dynamodb@vX.Y.Z
go get github.com/tochemey/ego-contrib/durablestore/cassandra@vX.Y.Z
go get github.com/tochemey/ego-contrib/eventstore/memory@vX.Y.Z
go get github.com/tochemey/ego-contrib/eventstore/postgres@vX.Y.Z
go get github.com/tochemey/ego-contrib/eventstore/sqlite@vX.Y.Z
go get github.com/tochemey/ego-contrib/offsetstore/memory@vX.Y.Z
go get github.com/tochemey/ego-contrib/offsetstore/postgres@vX.Y.Z
go get github.com/tochemey/ego-contrib/offsetstore/sqlite@vX.Y.Z
go get github.com/tochemey/ego-contrib/snapshotstore/postgres@vX.Y.Z
go get github.com/tochemey/ego-contrib/snapshotstore/sqlite@vX.Y.Z
```

Each module README has the schema to apply, the configuration and a complete example.

## Contributing

Read [contributing.md](./contributing.md) and [code_of_conduct.md](./code_of_conduct.md).
