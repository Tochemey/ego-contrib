VERSION 0.8

FROM tochemey/docker-go:1.22.2-3.1.0

test:
		BUILD --allow-privileged ./eventstore/github.com/hashicorp/memdb+test
		BUILD --allow-privileged ./eventstore/postgres+test
		BUILD --allow-privileged ./durablestore/postgres+test
		BUILD --allow-privileged ./durablestore/memory+test
		BUILD --allow-privileged ./offsetstore/github.com/hashicorp/memdb+test
		BUILD --allow-privileged ./offsetstore/postgres+test