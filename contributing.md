# Contributing

Contributions are welcome, from typo fixes to new backends. This guide covers how the repository is organised, how to build and test it, and what a pull request needs.

## Ways to contribute

1. Ask to become a collaborator and open pull requests directly against the repository.
2. Fork the repository, create a feature branch, and open a [pull request](https://help.github.com/articles/using-pull-requests) from your fork.

Open an issue before starting a large change, such as a new backend, so the design can be discussed first.
Read the [code of conduct](./code_of_conduct.md) before participating.

## Prerequisites

- Go 1.26 or later.
- Docker. The integration suites start PostgreSQL, Cassandra and DynamoDB Local with Testcontainers-Go and pull the images they need on first run.
- `make`.
- [golangci-lint](https://golangci-lint.run) v2 for linting on the host. CI runs v2.11.3. The Docker image described below ships the same version if you prefer not to install it.

## Repository layout

- `durablestore/`, `eventstore/`, `offsetstore/`, `snapshotstore/`: one directory per backend, each an independent Go module with its own `go.mod`, `.golangci.yml`, `README.md` and, where a schema applies, a `resources/` directory holding the DDL.
- `go.work`: workspace listing every module, used for local development and by the IDE.
- `Makefile`, `Dockerfile`: lint and test tasks.
- `.github/workflows/`: `build.yml` and `pull_request.yml` lint and test every module, `release.yml` tags every module on a release, `stale.yml` closes inactive issues and pull requests.
- `contributing.md`, `code_of_conduct.md`: this guide and the code of conduct.

## Workspace

The root `go.work` puts all modules in one workspace, so the IDE resolves imports across modules and a single command from the root covers all of them:

```bash
go build github.com/tochemey/ego-contrib/...
go vet github.com/tochemey/ego-contrib/...
```

Use the import-path pattern. `./...` is refused at the root because the root directory is not a module.
Commit `go.work.sum` together with `go.work`.

The workspace only serves local development. Every module is built, linted, tested and consumed on its own, which is what the Makefile and CI do.

## Lint and test

The `Makefile` runs each module on its own with its vendored dependencies:

```bash
make lint                       # every module
make test                       # every module
make test/eventstore/postgres   # one module
make fmt                        # go fmt in every module
make help                       # every target
```

Every target exists in a repository-wide form and in a per-module form, `<target>/<module path>`.

The Makefile and the CI pipelines set `GOWORK=off`, because workspace mode does not allow per-module vendoring.
Set it yourself when running `go mod vendor` or `go test -mod=vendor` by hand inside a module.

Tests run with the race detector and produce a `coverage.out` file in the module directory. Coverage files and `vendor/` directories are ignored by git.

The PostgreSQL stores have two kinds of tests: integration tests against a PostgreSQL container, and unit tests that run the store on a `pgxmock` pool through `New<Store>WithPool`. Add both when you change a store.

## Docker

The `Dockerfile` builds an image with the CI toolchain: Go, golangci-lint and the Docker CLI.

```bash
make docker-image                                   # build the image
make docker-lint                                    # lint every module inside it
make docker-test                                    # test every module inside it
make docker-run TARGET=test/eventstore/postgres     # run one target inside it
make docker-shell                                   # open a shell inside it
```

The host Docker socket is shared with the container so the suites can start their databases, and the host module cache is mounted to avoid repeated downloads.
On Docker Desktop, pass `TESTCONTAINERS_HOST_OVERRIDE=host.docker.internal` when the databases are not reachable from inside the container:

```bash
make docker-test TESTCONTAINERS_HOST_OVERRIDE=host.docker.internal
```

## Dependencies

Each module vendors its dependencies. The Makefile re-vendors before linting and testing, so a fresh clone needs no extra step.

```bash
make tidy       # go mod tidy in every module
make vendor     # go mod vendor in every module
make upgrade    # go get -t -u ./... and go mod tidy in every module
make clean      # remove vendor directories and coverage files
```

Keep the `go` directive of the modules in sync with the version CI uses, and keep every module on the same version of `github.com/tochemey/ego/v4`.

## Adding a module

1. Create the module in its own directory with its own `go.mod`, `.golangci.yml` (copy one from an existing module) and `README.md`. Put the schema, if any, in a `resources/` directory.
2. Register it in the `use` block of `go.work`, in the `MODULES` variable of the `Makefile`, and in the module matrices of `build.yml`, `pull_request.yml` and `release.yml` under `.github/workflows/`.
3. Add it to the module list of the root `README.md`.

## Commits and pull requests

Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/). CI rejects pull requests whose commits do not.

Before opening a pull request:

1. Run `make lint` and `make test`, or the per-module targets for the modules you touched.
2. Update the README of every module whose configuration, schema or API changed.
3. Keep the change focused. Unrelated cleanups belong in their own pull request.

CI lints and tests every module on each pull request and on every push to `main`.

## Releases

The repository follows [Semantic Versioning](https://semver.org). All modules are released together under the same version.
Maintainers cut a release by pushing a root tag `vX.Y.Z`; the release workflow then creates a `<module>/vX.Y.Z` tag for every module on the same commit, which is what `go get` resolves.
