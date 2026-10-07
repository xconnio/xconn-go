lint:
	golangci-lint run

format:
	golangci-lint fmt

test:
	go test -count=1 . ./xconn/... -v

integration:
	go test -count=1 ./interoptests -v

build:
	go build -o nxt-router ./cmd/nxt

run:
	go run ./cmd/nxt start

build-xconn:
	CGO_ENABLED=0 go build -o xconn-bin ./cmd/xconn

run-xconn:
	go run ./cmd/xconn
