.PHONY: build test lint e2e snapshot release-check clean

BINARY  := kubectl-konch
PKG     := github.com/christophersherman/magic-konch
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/kubectl-konch

test:
	go test -race -count=1 ./...

lint:
	golangci-lint run --timeout=5m

e2e:
	go test -tags=e2e ./test/e2e/...

snapshot:
	goreleaser release --snapshot --clean --skip=publish

release-check:
	goreleaser check

clean:
	rm -f $(BINARY)
	rm -rf dist/
