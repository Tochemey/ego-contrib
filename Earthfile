VERSION 0.8

FROM golang:1.25.3-alpine

# install gcc dependencies into alpine for CGO
RUN apk --no-cache add git ca-certificates gcc musl-dev libc-dev binutils-gold curl openssh

# install docker tools
# https://docs.docker.com/engine/install/debian/
RUN apk add --update --no-cache docker

# install linter
# binary will be $(go env GOPATH)/bin/golangci-lint
RUN curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh | sh -s -- -b $(go env GOPATH)/bin v2.5.0
RUN golangci-lint --version

test:
		BUILD --allow-privileged ./eventstore/memory+test
		BUILD --allow-privileged ./eventstore/postgres+test
		BUILD --allow-privileged ./durablestore/dynamodb+test
		BUILD --allow-privileged ./durablestore/postgres+test
		BUILD --allow-privileged ./durablestore/memory+test
		BUILD --allow-privileged ./offsetstore/memory+test
		BUILD --allow-privileged ./offsetstore/postgres+test