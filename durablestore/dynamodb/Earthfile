VERSION 0.8

FROM tochemey/docker-go:1.23.4-5.1.1

test:
  BUILD +lint
  BUILD +local-test

code:
    WORKDIR /app

    # download deps
    COPY go.mod go.sum ./
    RUN go mod download -x

    # copy in code
    COPY --dir . ./

vendor:
    FROM +code

    RUN go mod vendor
    SAVE ARTIFACT /app /files

lint:
    FROM +vendor

    COPY .golangci.yml ./
    # Runs golangci-lint with settings:
    RUN golangci-lint run --timeout 10m

local-test:
    FROM +vendor

    WITH DOCKER --pull amazon/dynamodb-local:2.5.4
        RUN go test -v -mod=vendor ./...  -timeout 0 -race -v  -coverprofile=coverage.out -covermode=atomic -coverpkg=./...
    END

    SAVE ARTIFACT coverage.out AS LOCAL coverage.out
