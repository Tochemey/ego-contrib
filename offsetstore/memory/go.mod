module github.com/tochemey/ego-contrib/offsetstore/memory

go 1.26.0

require (
	github.com/google/uuid v1.6.0
	github.com/hashicorp/go-memdb v1.3.5
	github.com/stretchr/testify v1.12.1
	go.uber.org/atomic v1.11.0
	google.golang.org/protobuf v1.36.12
)

require go.yaml.in/yaml/v3 v3.0.5 // indirect

require (
	github.com/hashicorp/go-immutable-radix v1.3.1 // indirect
	github.com/hashicorp/golang-lru v1.0.2 // indirect
	github.com/tochemey/ego/v4 v4.4.3
)
