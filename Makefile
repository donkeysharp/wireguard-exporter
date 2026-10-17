BINARY   := wireguard_exporter
PKG      := github.com/donkeysharp/wireguard-exporter
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
REVISION ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS  := -s -w -X main.version=$(VERSION) -X main.revision=$(REVISION)
GOFLAGS  := -trimpath
PLATFORMS := linux/amd64 linux/arm64 linux/arm

export CGO_ENABLED := 0
export GOTOOLCHAIN := local

.PHONY: all build test vet fmt lint cross snapshot clean

all: fmt vet test build

build:
	go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o bin/$(BINARY) ./cmd/$(BINARY)

test:
	CGO_ENABLED=1 go test -race -count=1 ./...

vet:
	go vet ./...

fmt:
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo "run: gofmt -w ."; exit 1; }

lint:
	@command -v golangci-lint >/dev/null || { echo "golangci-lint not installed"; exit 1; }
	golangci-lint run ./...

cross:
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		echo "building $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch go build $(GOFLAGS) -ldflags '$(LDFLAGS)' \
			-o bin/$(BINARY)-$$os-$$arch ./cmd/$(BINARY) || exit 1; \
	done

snapshot:
	@command -v goreleaser >/dev/null || { echo "goreleaser not installed: https://goreleaser.com/install/"; exit 1; }
	goreleaser release --snapshot --clean

clean:
	rm -rf bin dist
