# Development image used by the docker-* targets of the Makefile.
#
# It carries the same toolchain as the CI pipelines: Go, golangci-lint and the Docker
# CLI. Share the host Docker socket with the container so the testcontainers-based
# suites can start the databases they need.
ARG GO_VERSION=1.26
FROM golang:${GO_VERSION}-alpine

ARG GOLANGCI_LINT_VERSION=v2.11.3

# gcc and musl-dev are required by CGO, which the race detector relies on
RUN apk add --no-cache bash ca-certificates curl docker-cli gcc git make musl-dev

RUN curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh \
    | sh -s -- -b "$(go env GOPATH)/bin" "${GOLANGCI_LINT_VERSION}" \
    && golangci-lint --version

WORKDIR /workspace
