lint:
	golangci-lint run

format:
	golangci-lint fmt

test:
	go test -count=1 . -v

integration:
	go test -count=1 ./interoptests -v

build:
	go build -o nxt-router ./cmd/nxt

run:
	go run ./cmd/nxt start
