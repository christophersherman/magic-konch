.PHONY: build test lint e2e clean

BINARY := kubectl-konch
PKG    := github.com/christophersherman/magic-konch

build:
	go build -trimpath -o $(BINARY) ./cmd/kubectl-konch

test:
	go test ./...

lint:
	golangci-lint run

e2e:
	go test -tags=e2e ./test/e2e/...

clean:
	rm -f $(BINARY)
	rm -rf dist/
