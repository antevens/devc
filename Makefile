VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
PREFIX ?= $(HOME)/.local
STATICCHECK_VERSION := v0.8.2
SHELL_SCRIPTS := $(wildcard internal/assets/files/*.sh)

.PHONY: all build install test fmt lint vet staticcheck shellcheck whitespace clean

all: lint test build

build:
	go build -ldflags "$(LDFLAGS)" -o bin/devc ./cmd/devc

install: build
	install -D -m 0755 bin/devc $(PREFIX)/bin/devc

test:
	go test ./...

fmt:
	gofmt -s -w .

lint: vet staticcheck shellcheck whitespace
	@test -z "$$(gofmt -s -l .)" || { gofmt -s -l .; echo "run make fmt"; exit 1; }

vet:
	go vet ./...

staticcheck:
	go run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) ./...

shellcheck:
	shellcheck $(SHELL_SCRIPTS)
	go run ./cmd/devc completion bash | shellcheck -

whitespace:
	@! git grep -nI '[[:space:]]$$' -- . || { echo "trailing whitespace"; exit 1; }

clean:
	rm -rf bin
