lint:
	golangci-lint run

test:
	go test -count=1 ./... -v

build:
	go build ./cmd/xconn

run:
	go run ./cmd/xconn start

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
