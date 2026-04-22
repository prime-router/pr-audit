.PHONY: build test lint vet fmt gocloc clean cover help

BIN     := pr-audit
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "v0.1.0-dev")
LDFLAGS := -ldflags "-s -w -X main.version=$(VERSION)"

help:
	@echo "Targets: build test lint vet fmt gocloc cover clean"

build:
	go build $(LDFLAGS) -o $(BIN) ./cmd/pr-audit

test:
	go test -race -cover ./...

cover:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

fmt:
	gofmt -w .

vet:
	go vet ./...

lint: vet
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt wants to change:"; echo "$$out"; exit 1; fi

gocloc:
	@command -v gocloc >/dev/null 2>&1 || { echo "gocloc not installed: go install github.com/hhatto/gocloc/cmd/gocloc@latest"; exit 1; }
	gocloc --not-match-d='testdata|vendor' --include-lang=Go .

clean:
	rm -f $(BIN) $(BIN).exe coverage.out coverage.html
