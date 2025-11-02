VERSION 0.8

FROM golang:1.25.3-alpine

# install gcc dependencies into alpine for CGO
RUN apk --no-cache add git ca-certificates gcc musl-dev libc-dev binutils-gold curl openssh

# install docker tools
# https://docs.docker.com/engine/install/debian/
RUN apk add --update --no-cache docker

# install the go generator plugins
RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
RUN go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
RUN export PATH="$PATH:$(go env GOPATH)/bin"

# install buf from source
RUN GO111MODULE=on GOBIN=/usr/local/bin go install github.com/bufbuild/buf/cmd/buf@v1.58.0

# install the various tools to generate connect-go
RUN go install github.com/fullstorydev/grpcurl/cmd/grpcurl@latest
RUN go install connectrpc.com/connect/cmd/protoc-gen-connect-go@latest

# install linter
# binary will be $(go env GOPATH)/bin/golangci-lint
RUN curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh | sh -s -- -b $(go env GOPATH)/bin v2.5.0
RUN golangci-lint --version

# install vektra/mockery
RUN go install github.com/vektra/mockery/v2@v2.53.2

test:
		BUILD --allow-privileged ./eventstore/memory+test
		BUILD --allow-privileged ./eventstore/postgres+test
		BUILD --allow-privileged ./durablestore/dynamodb+test
		BUILD --allow-privileged ./durablestore/postgres+test
		BUILD --allow-privileged ./durablestore/memory+test
		BUILD --allow-privileged ./offsetstore/memory+test
		BUILD --allow-privileged ./offsetstore/postgres+test