# eGo Contrib

[![build](https://img.shields.io/github/actions/workflow/status/Tochemey/ego-contrib/build.yml?branch=main)](https://github.com/Tochemey/ego-contrib/actions/workflows/build.yml)
[![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/tochemey/ego-contrib)](https://go.dev/doc/install)

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
| PostgreSQL | --     | [Schema](./snapshotstore/postgres/resources/snapshotstore_postgres.sql) | `go get github.com/tochemey/ego-contrib/snapshotstore/postgres` |

Missing a backend you need? [Open an issue](https://github.com/Tochemey/ego-contrib/issues/new) or propose one -- contributions welcome!

## Getting Started

1. Install the module you need:
   ```bash
   go get github.com/tochemey/ego-contrib/eventstore/postgres
   ```
   Replace the path with any package from the tables above.

2. Prepare the backing service.
   Apply the SQL schema (PostgreSQL) or provision the DynamoDB table. Schemas live in each module's `resources/` folder.

3. Wire the store into your eGo system:
   ```go
   package main

   import (
       "context"
       "log"

       eventpg "github.com/tochemey/ego-contrib/eventstore/postgres"
       durablemem "github.com/tochemey/ego-contrib/durablestore/memory"
       "github.com/tochemey/ego/v4/persistence"
   )

   func main() {
       ctx := context.Background()

       events := eventpg.NewEventsStore(&eventpg.Config{
           DBHost: "127.0.0.1", DBPort: 5432, DBName: "ego", DBUser: "ego", DBPassword: "secret",
       })
       if err := events.Connect(ctx); err != nil {
           log.Fatalf("connect event store: %v", err)
       }

       durable := durablemem.NewStateStore()
       if err := durable.Connect(ctx); err != nil {
           log.Fatalf("connect durable store: %v", err)
       }

       // supply to your eGo framework...
       var _ persistence.EventsStore = events
       var _ persistence.StateStore = durable
   }
   ```

4. Explore module guides: each README covers backend-specific setup, examples, and testing tips.

## Repository Structure

- `durablestore/` -- durable state stores (memory, PostgreSQL, DynamoDB, Cassandra)
- `eventstore/` -- event journals for event-sourced behaviors
- `offsetstore/` -- projection offset stores for eGo projections
- `snapshotstore/` -- snapshot stores for eGo snapshot-based persistence
- `Earthfile` -- builds via [Earthly](https://earthly.dev)
- `contributing.md`, `code_of_conduct.md` -- community guidelines

## Development Workflow

- Uses [Semantic Versioning](https://semver.org) and [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/).
- Primary CI/local workflows in the `Earthfile`. Run:
  ```bash
  earthly +test
  ```
  to lint and test all modules.
- External-service modules ship Testcontainers-Go helpers for integration tests -- see each backend's `testkit.go`.

## Contributing

We welcome everything from typo fixes to brand-new backends.

1. Read [code_of_conduct.md](./code_of_conduct.md) and [contributing.md](./contributing.md).
2. For larger changes, open an issue or draft PR to align early.
3. Follow existing package layout and naming.
4. Open a PR. If you run Earthly builds from a fork, export `DOCKER_USER` and `DOCKER_PASS`.

Prefer not to fork? Ask for collaborator access and we'll streamline your flow.
