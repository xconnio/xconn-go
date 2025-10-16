lint:
	golangci-lint run

format:
	golangci-lint fmt

test:
	go test -count=1 ./... -v

build-docs:
	mkdir -p site/xconn/
	mkdocs build -d site/xconn/go

run-docs:
	mkdocs serve

clean-docs:
	rm -rf site/

release-local:
	 goreleaser release --snapshot --clean

release:
	goreleaser release

build:
	go build ./cmd/nxt

run:
	go run ./cmd/nxt start
