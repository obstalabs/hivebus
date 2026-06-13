BINARY      := hivebus
MODULE      := github.com/obstalabs/hivebus
VERSION     ?= $(shell git describe --tags --match 'v*' --always --dirty 2>/dev/null || echo dev)
VERSION_NUM := $(VERSION:v%=%)
COMMIT      := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILD_DATE  := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS     := -X $(MODULE)/internal/cli.Version=$(VERSION_NUM) -X $(MODULE)/internal/cli.Commit=$(COMMIT) -X $(MODULE)/internal/cli.BuildDate=$(BUILD_DATE) -X $(MODULE)/internal/runtime.BuiltInAPIVerifyKey=$(HIVEBUS_API_VERIFY_KEY)

.PHONY: build test lint vet clean

build:
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/hivebus

test:
	go test -race -cover ./...

lint:
	golangci-lint run ./...

vet:
	go vet ./...

clean:
	rm -r bin/ 2>/dev/null || true

.DEFAULT_GOAL := build
