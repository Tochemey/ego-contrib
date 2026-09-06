# Development tasks for the modules of this repository.
#
# Every module is an independent Go module. A target either runs one step across all
# of them (make lint, make test) or in a single module (make lint/eventstore/postgres,
# make test/eventstore/postgres).
#
# The docker-* targets run the same steps inside the dev image built from the
# Dockerfile. The host Docker socket is shared with that container so the
# testcontainers-based suites can start the databases they need.

SHELL := /bin/bash
.DEFAULT_GOAL := help

# go.work only serves local development. Every target here works on one module at a
# time with its vendored dependencies, which workspace mode does not allow.
export GOWORK := off

MODULES := \
	eventstore/memory \
	eventstore/postgres \
	eventstore/sqlite \
	durablestore/memory \
	durablestore/postgres \
	durablestore/sqlite \
	durablestore/dynamodb \
	durablestore/cassandra \
	offsetstore/memory \
	offsetstore/postgres \
	offsetstore/sqlite \
	snapshotstore/postgres \
	snapshotstore/sqlite

GO_TEST_FLAGS       ?= -timeout 0 -race -v -coverprofile=coverage.out -covermode=atomic -coverpkg=./...
GOLANGCI_LINT_FLAGS ?= --timeout 10m

DOCKER_IMAGE  ?= ego-contrib-dev
DOCKER_SOCKET ?= /var/run/docker.sock
GOMODCACHE    ?= $(or $(shell go env GOMODCACHE 2>/dev/null),$(HOME)/go/pkg/mod)
# Set it to host.docker.internal on Docker Desktop when the containers started by the
# tests cannot be reached from inside the dev image.
TESTCONTAINERS_HOST_OVERRIDE ?=

DOCKER_TTY := $(shell [ -t 0 ] && echo "-it" || echo "-i")
DOCKER_RUN  = docker run --rm $(DOCKER_TTY) \
	-v "$(CURDIR):/workspace" -w /workspace \
	-v "$(DOCKER_SOCKET):/var/run/docker.sock" \
	-v "$(GOMODCACHE):/go/pkg/mod" \
	$(if $(TESTCONTAINERS_HOST_OVERRIDE),-e TESTCONTAINERS_HOST_OVERRIDE=$(TESTCONTAINERS_HOST_OVERRIDE)) \
	$(DOCKER_IMAGE)

.PHONY: help modules tidy vendor fmt lint test upgrade clean docker-image docker-lint docker-test docker-run docker-shell

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'
	@echo
	@echo "Per-module targets: tidy/, vendor/, fmt/, lint/, test/ and upgrade/ followed by a module path."
	@echo "Example: make test/eventstore/postgres"

modules: ## List the modules
	@printf '%s\n' $(MODULES)

tidy: $(addprefix tidy/,$(MODULES)) ## Run go mod tidy in every module
vendor: $(addprefix vendor/,$(MODULES)) ## Run go mod vendor in every module
fmt: $(addprefix fmt/,$(MODULES)) ## Format the Go sources of every module
lint: $(addprefix lint/,$(MODULES)) ## Lint every module
test: $(addprefix test/,$(MODULES)) ## Test every module (Docker is needed for the testcontainers suites)
upgrade: $(addprefix upgrade/,$(MODULES)) ## Upgrade the dependencies of every module

clean: ## Remove the vendor directories and coverage files
	@for module in $(MODULES); do rm -rf "$$module/vendor" "$$module/coverage.out"; done

# MODULE_RULES defines the per-module targets for the module given as argument
define MODULE_RULES
.PHONY: tidy/$(1) vendor/$(1) fmt/$(1) lint/$(1) test/$(1) upgrade/$(1)

tidy/$(1):
	cd $(1) && go mod tidy

vendor/$(1):
	cd $(1) && go mod vendor

fmt/$(1):
	cd $(1) && go fmt ./...

lint/$(1): vendor/$(1)
	cd $(1) && golangci-lint run $$(GOLANGCI_LINT_FLAGS)

test/$(1): vendor/$(1)
	cd $(1) && go test -mod=vendor ./... $$(GO_TEST_FLAGS)

upgrade/$(1):
	cd $(1) && go get -t -u ./... && go mod tidy
endef

$(foreach module,$(MODULES),$(eval $(call MODULE_RULES,$(module))))

docker-image: ## Build the dev image (Go, golangci-lint and the Docker CLI)
	docker build -t $(DOCKER_IMAGE) .

docker-lint: docker-image ## Lint every module inside the dev image
	$(DOCKER_RUN) make lint

docker-test: docker-image ## Test every module inside the dev image
	$(DOCKER_RUN) make test

docker-run: docker-image ## Run a single target inside the dev image, e.g. make docker-run TARGET=test/eventstore/postgres
	$(DOCKER_RUN) make $(TARGET)

docker-shell: docker-image ## Open a shell inside the dev image
	$(DOCKER_RUN) bash
