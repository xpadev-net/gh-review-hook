.PHONY: build test clean lint coverage ci

BINARY=gh-review-hook

# Use external linker to ensure LC_UUID is present (required for macOS 15+).
# This workaround is needed for Go < 1.22.
# With Go 1.22+, the standard `go build` works without this flag.
build:
	go build -ldflags="-linkmode=external" -o $(BINARY) ./cmd/gh-review-hook

test:
	go test -v -race ./...

lint:
	go vet ./...

coverage:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

ci: lint test

clean:
	rm -f $(BINARY) coverage.out
