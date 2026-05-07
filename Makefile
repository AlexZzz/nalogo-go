.PHONY: build test lint coverage

build:
	go build ./...

test:
	go test ./...

lint:
	go vet ./...
	@which staticcheck > /dev/null 2>&1 && staticcheck ./... || true

coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1
