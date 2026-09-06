# Contributions are welcome

The project adheres to [Semantic Versioning](https://semver.org) and [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/).
Every backend is its own Go module and the root `go.work` puts them in one workspace for local development.
Lint and test tasks are driven by the `Makefile`, which runs each module on its own with `GOWORK=off` and its vendored dependencies. Docker must be running for the integration suites.

There are two ways you can become a contributor:

1. Request to become a collaborator and then you can just open pull requests against the repository without forking it.
2. Follow these steps
   - Fork the repository
   - Create a feature branch
   - Submit a [pull request](https://help.github.com/articles/using-pull-requests)

## Test & Linter

Prior to submitting a [pull request](https://help.github.com/articles/using-pull-requests), please run:

```bash
make lint
make test
```

Both accept a single module as well, e.g. `make test/eventstore/postgres`. Run `make help` for the full list of targets.

## Adding a module

1. Create the module in its own directory with its own `go.mod`, `.golangci.yml` and `README.md`.
2. Add it to the `use` block of `go.work`, to the `MODULES` variable of the `Makefile`, and to the module matrices of the workflows under `.github/workflows`.
