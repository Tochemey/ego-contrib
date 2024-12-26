# eGo contrib

Collection of 3rd-party package for [eGo](https://github.com/Tochemey/ego)

## Content

- [Events Stores](./eventstore): Packages providing all event stores that can be used with eGo when building an events-sourced application
    - [Hashicorp Memdb](./eventstore/github.com/hashicorp/memdb): (for testing purpose only)
    - [Postgres](./eventstore/postgres): fully functional. Schema can be found [here](./eventstore/postgres/resources/eventstore_postgres.sql). The schema needs to be created before using the store.
- [Durable Stores](./durablestore): Packages providing all durable state stores that can be used with eGo DurableStateEntity.
    - [Memory](./durablestore/memory): (for testing purpose only)
    - [Postgres](./durablestore/postgres): fully functional. Schema can be found [here](./durablestore/postgres/resources/durablestore_postgres.sql). The schema needs to be created before using the store.

## Contribution

Contributions are welcome!
The project adheres to [Semantic Versioning](https://semver.org)
and [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/).
This repo uses [Earthly](https://earthly.dev/get-earthly).

There are two ways you can become a contributor:

1. Request to become a collaborator, and then you can just open pull requests against the repository without forking it.
2. Follow these steps

- Fork the repository
- Create a feature branch
- Set your docker credentials on your fork using the following secret names: `DOCKER_USER` and `DOCKER_PASS`
- Submit a [pull request](https://help.github.com/articles/using-pull-requests)

## Test & Linter

Prior to submitting a [pull request](https://help.github.com/articles/using-pull-requests), please run:

```bash
earthly +test
```
