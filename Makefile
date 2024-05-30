lint:
	golangci-lint run

test:
	go test -count=1 ./... -v

run:
	go run ./cmd/xconn

build-docs:
	mkdir -p site/xconn/
	mkdocs build -d site/xconn/go

run-docs:
	mkdocs serve

clean-docs:
	rm -rf site/
