BINARY  := pipeliner
PKG     := ./...
COVER   := coverage.out
VERSION := $(shell git describe --tags --dirty --always 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

.PHONY: build test lint vet cover clean

build:
	go build $(LDFLAGS) -o $(BINARY) ./cmd/pipeliner

test:
	go test -race $(PKG)

vet:
	go vet $(PKG)

lint:
	golangci-lint run $(PKG)

cover:
	go test -race -coverprofile=$(COVER) $(PKG)
	go tool cover -html=$(COVER) -o coverage.html

clean:
	rm -f $(BINARY) $(COVER) coverage.html
