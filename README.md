# eGo contrib

[![build](https://img.shields.io/github/actions/workflow/status/Tochemey/ego-contrib/build.yml?branch=main)](https://github.com/Tochemey/ego-contrib/actions/workflows/build.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/tochemey/ego.svg)](https://pkg.go.dev/github.com/tochemey/ego-contrib)
[![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/tochemey/ego-contrib)](https://go.dev/doc/install)

Collection of 3rd-party packages for [eGo](https://github.com/Tochemey/ego)

## Content

- [Events Stores](./eventstore): Contains data store packages to build [event-sourcing](https://github.com/Tochemey/ego?tab=readme-ov-file#event-sourced-behavior) applications with eGo.
    - [Memory](./eventstore/memory): It is powered by [hashicorp memdb](https://github.com/hashicorp/go-memdb).
    - [Postgres](./eventstore/postgres): Schema can be found [here](./eventstore/postgres/resources/eventstore_postgres.sql). The schema needs to be created before using the store.
- [Durable Stores](./durablestore): Contains data store packages to build non-event-sourcing applications with eGo. See [reference](https://github.com/Tochemey/ego?tab=readme-ov-file#durable-state-behavior).
    - [Memory](./durablestore/memory): This store should only be used in testing. It is powered by go standard thread-safe map.
    - [Postgres](./durablestore/postgres): Schema can be found [here](./durablestore/postgres/resources/durablestore_postgres.sql). The schema needs to be created before using the store.
- [Offset Store](./offsetstore): Packages providing all offset stores for [Projections](https://github.com/Tochemey/ego?tab=readme-ov-file#projection).
  - [Memory](./offsetstore/memory): It is powered by [hashicorp memdb](https://github.com/hashicorp/go-memdb).
  - [Postgres](./offsetstore/postgres): Schema can be found [here](./offsetstore/postgres/resources/offsetstore_postgres.sql). The schema needs to be created before using the store.

## Contribution

Contributions are welcome!
The project adheres to [Semantic Versioning](https://semver.org)
and [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/).
This repo uses [Earthly](https://earthly.dev/get-earthly).

There are two ways you can become a contributor:

1. Request to become a collaborator, and then you can just open pull requests against the repository without forking it.
2. Follow these steps

- Fork the repository
- Create a feature branch by following the existing package and naming patterns
- Set your docker credentials on your fork using the following secret names: `DOCKER_USER` and `DOCKER_PASS`
- Submit a [pull request](https://help.github.com/articles/using-pull-requests)

## Test & Linter

Prior to submitting a [pull request](https://help.github.com/articles/using-pull-requests), please run:

```bash
earthly +test
```
