# eGo Contrib

[![build](https://img.shields.io/github/actions/workflow/status/Tochemey/ego-contrib/build.yml?branch=main)](https://github.com/Tochemey/ego-contrib/actions/workflows/build.yml)
[![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/tochemey/ego-contrib)](https://go.dev/doc/install)

Collection of community-maintained storage backends and tooling for the [eGo framework](https://github.com/Tochemey/ego). Each module plugs into eGo’s persistence APIs so you can mix and match durable state, event journals, and projection offset stores without rewriting infrastructure code.

## Available Modules

| Category | Backend | Highlights | Documentation |
|----------|---------|------------|---------------|
| Durable State | Memory | Zero-dependency state snapshots for tests and prototypes | [README](./durablestore/memory/README.md) |
| Durable State | PostgreSQL | `pgx` powered snapshots with `INSERT … ON CONFLICT` upserts | [README](./durablestore/postgres/README.md) · [Schema](./durablestore/postgres/resources/durablestore_postgres.sql) |
| Durable State | Amazon DynamoDB | Serverless persistence using AWS SDK v2 | [README](./durablestore/dynamodb/README.md) |
| Event Store | Memory | HashiCorp `memdb` journal for event-sourced tests | [README](./eventstore/memory/README.md) |
| Event Store | PostgreSQL | Batched inserts and replay queries via `pgx` | [README](./eventstore/postgres/README.md) · [Schema](./eventstore/postgres/resources/eventstore_postgres.sql) |
| Offset Store | Memory | In-memory projection offsets with `memdb` | [README](./offsetstore/memory/README.md) |
| Offset Store | PostgreSQL | Transactional offset management on relational storage | [README](./offsetstore/postgres/README.md) · [Schema](./offsetstore/postgres/resources/offsetstore_postgres.sql) |

Looking for a backend that isn’t listed yet? [Open an issue](https://github.com/Tochemey/ego-contrib/issues/new) or send a proposal—we welcome contributions for additional databases and cloud services.

## Getting Started

1. **Install the module you need.**
   ```bash
   go get github.com/tochemey/ego-contrib/eventstore/postgres
   ```
   Replace the path with any of the packages listed above.

2. **Prepare the backing service.**  
   Apply the SQL schema (for PostgreSQL stores) or provision the appropriate table (for DynamoDB). Schema files live under each module’s `resources/` folder.

3. **Wire the store into your actor system.**
   ```go
   package main

   import (
   	"context"
   	"log"

   	eventpg "github.com/tochemey/ego-contrib/eventstore/postgres"
   	durablemem "github.com/tochemey/ego-contrib/durablestore/memory"
   	"github.com/tochemey/ego/v3/persistence"
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

   	// supply to your eGo actor system…
   	var _ persistence.EventsStore = events
   	var _ persistence.StateStore = durable
   }
   ```

4. **Explore module guides.**  
   Each README includes backend-specific tips, example code, and testing notes.

## Repository Structure

- `durablestore/` – durable state stores for eGo actors (memory, PostgreSQL, DynamoDB).
- `eventstore/` – event journal implementations for event-sourced behaviors.
- `offsetstore/` – projection offset stores to drive eGo projections.
- `Earthfile` – build orchestrations leveraging [Earthly](https://earthly.dev).
- `contributing.md`, `code_of_conduct.md` – community guidance and policies.

## Development Workflow

- The project adheres to [Semantic Versioning](https://semver.org) and [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/).
- Primary CI and local workflows are defined in the `Earthfile`. Run:
  ```bash
  earthly +test
  ```
  to execute linting and tests across every module.
- Modules that depend on external services ship with Dockertest-powered helpers for integration tests—see each backend’s `testkit.go`.

## Contributing

We love contributions ranging from typo fixes to new storage backends. To get started:

1. Review [code_of_conduct.md](./code_of_conduct.md) and [contributing.md](./contributing.md).
2. Discuss substantial changes in an issue or draft PR to align early.
3. Follow existing package layout and naming conventions.
4. Open a pull request. If you maintain Docker credentials (for Earthly builds) on your fork, export them as `DOCKER_USER` and `DOCKER_PASS`.

Prefer to collaborate without forking? Request collaborator access, and we can streamline your workflow.
