# eGo Contrib

[![build](https://img.shields.io/github/actions/workflow/status/Tochemey/ego-contrib/build.yml?branch=main)](https://github.com/Tochemey/ego-contrib/actions/workflows/build.yml)
[![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/tochemey/ego-contrib?filename=eventstore%2Fpostgres%2Fgo.mod)](https://go.dev/doc/install)

Community-maintained storage backends and tooling for [eGo](https://github.com/Tochemey/ego).
Plug any module into eGo's persistence APIs and mix durable state, event journals, and projection offsets without infrastructure rewrites.

## Modules

### Durable State Stores

| Backend          | README                                       | Schema                                                                | Install                                                         |
|------------------|----------------------------------------------|-----------------------------------------------------------------------|-----------------------------------------------------------------|
| Memory           | [README](./durablestore/memory/README.md)    | --                                                                    | `go get github.com/tochemey/ego-contrib/durablestore/memory`    |
| PostgreSQL       | [README](./durablestore/postgres/README.md)  | [Schema](./durablestore/postgres/resources/durablestore_postgres.sql) | `go get github.com/tochemey/ego-contrib/durablestore/postgres`  |
| DynamoDB  | [README](./durablestore/dynamodb/README.md)  | --                                                                    | `go get github.com/tochemey/ego-contrib/durablestore/dynamodb`  |
| Cassandra | [README](./durablestore/cassandra/README.md) | [Schema](./durablestore/cassandra/resources/states_store.sql)         | `go get github.com/tochemey/ego-contrib/durablestore/cassandra` |

### Event Stores

| Backend    | README                                    | Schema                                                            | Install                                                      |
|------------|-------------------------------------------|-------------------------------------------------------------------|--------------------------------------------------------------|
| Memory     | [README](./eventstore/memory/README.md)   | --                                                                | `go get github.com/tochemey/ego-contrib/eventstore/memory`   |
| PostgreSQL | [README](./eventstore/postgres/README.md) | [Schema](./eventstore/postgres/resources/eventstore_postgres.sql) | `go get github.com/tochemey/ego-contrib/eventstore/postgres` |

### Offset Stores

| Backend    | README                                     | Schema                                                              | Install                                                       |
|------------|--------------------------------------------|---------------------------------------------------------------------|---------------------------------------------------------------|
| Memory     | [README](./offsetstore/memory/README.md)   | --                                                                  | `go get github.com/tochemey/ego-contrib/offsetstore/memory`   |
| PostgreSQL | [README](./offsetstore/postgres/README.md) | [Schema](./offsetstore/postgres/resources/offsetstore_postgres.sql) | `go get github.com/tochemey/ego-contrib/offsetstore/postgres` |

### Snapshot Stores

| Backend    | README | Schema                                                                  | Install                                                         |
|------------|--------|-------------------------------------------------------------------------|-----------------------------------------------------------------|
| PostgreSQL | [README](./snapshotstore/postgres/README.md) | [Schema](./snapshotstore/postgres/resources/snapshotstore_postgres.sql) | `go get github.com/tochemey/ego-contrib/snapshotstore/postgres` |

Missing a backend you need? [Open an issue](https://github.com/Tochemey/ego-contrib/issues/new) or propose one -- contributions welcome!

## Getting Started

1. Pick a module from the tables above and `go get` it.
2. Apply the SQL schema or provision the backing service. Schemas live in each module's `resources/` folder.
3. Wire the store into your eGo system. See the [eGo documentation](https://github.com/Tochemey/ego) and each module's README for usage examples.
4. The PostgreSQL stores either build their own `pgxpool.Pool` from their `Config` or run on a pool you provide, so one pool can back all of them. See the "Bringing your own connection pool" section of each PostgreSQL README.

## Repository Structure

- `durablestore/` -- durable state stores (memory, PostgreSQL, DynamoDB, Cassandra)
- `eventstore/` -- event journals for event-sourced behaviors
- `offsetstore/` -- projection offset stores for eGo projections
- `snapshotstore/` -- snapshot stores for eGo snapshot-based persistence
- `go.work` -- Go workspace listing every module, for local development and the IDE
- `Makefile`, `Dockerfile` -- lint and test tasks, runnable on the host or inside the Docker dev image
- `contributing.md`, `code_of_conduct.md` -- community guidelines

## Development Workflow

- Uses [Semantic Versioning](https://semver.org) and [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/).
- Every backend is its own Go module. The root `go.work` puts them in one workspace, so the IDE resolves
  imports across modules and a single command from the repository root covers all of them using the module cache:
  ```bash
  go build github.com/tochemey/ego-contrib/...
  go vet github.com/tochemey/ego-contrib/...
  ```
  Use the import-path pattern: `./...` is refused at the root because the root directory itself is not a module.
  `go.work.sum` is committed next to `go.work`. When you add a module, list it in `go.work`, in the `MODULES`
  variable of the `Makefile`, and in the CI matrices.
- The workspace is a development convenience only. The `Makefile` and the CI pipelines run with `GOWORK=off`
  so each module is built, linted and tested on its own with its vendored dependencies, exactly as it is
  consumed. Set `GOWORK=off` yourself when running `go mod vendor` or `go test -mod=vendor` by hand inside a module.
- Lint and test tasks live in the `Makefile`. Run:
  ```bash
  make lint
  make test
  ```
  to lint and test all modules, or target a single module, e.g. `make test/eventstore/postgres`.
  `make help` lists every target.
- The integration suites start their databases through Testcontainers-Go, so Docker must be running.
- To run the same tasks inside a container with the CI toolchain, use `make docker-lint` and `make docker-test`.
  The host Docker socket is shared with the container so the suites can start their databases.
  On Docker Desktop, pass `TESTCONTAINERS_HOST_OVERRIDE=host.docker.internal` if the databases are not reachable from inside the container.

## Contributing

We welcome everything from typo fixes to brand-new backends.

1. Read [code_of_conduct.md](./code_of_conduct.md) and [contributing.md](./contributing.md).
2. For larger changes, open an issue or draft PR to align early.
3. Follow existing package layout and naming.
4. Open a PR.

Prefer not to fork? Ask for collaborator access and we'll streamline your flow.
