VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
PREFIX ?= $(HOME)/.local

.PHONY: build install test fmt lint clean

build:
	go build -ldflags "$(LDFLAGS)" -o devc .

install: build
	install -D -m 0755 devc $(PREFIX)/bin/devc

test:
	go test ./...

fmt:
	gofmt -w .

lint:
	go vet ./...
	staticcheck ./...

clean:
	rm -f devc
