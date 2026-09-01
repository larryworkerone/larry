# Larry 探针 — Makefile
VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.Version=$(VERSION)
PKG := github.com/larry-probe/larry
LDFLAGS_AGENT := -X $(PKG)/internal/agent.Version=$(VERSION)

.PHONY: all server agent build test vet fmt clean run cross

all: build

build: server agent

server:
	go build -ldflags "$(LDFLAGS) $(LDFLAGS_AGENT)" -o bin/larry-server ./cmd/larry-server

agent:
	go build -ldflags "$(LDFLAGS) $(LDFLAGS_AGENT)" -o bin/larry-agent ./cmd/larry-agent

# Cross-compile single-file binaries for the platforms servers actually run on.
# CGO is disabled so the agent links no libc — a static binary that runs on any
# glibc/musl/bionic host, matching how Nezha/Komari ship agents.
CROSS = \
  linux/amd64 linux/arm64 linux/386 \
  darwin/amd64 darwin/arm64 \
  windows/amd64 windows/arm64 \
  freebsd/amd64

cross:
	@for t in $(CROSS); do \
	  os=$${t%/*}; arch=$${t#*/}; \
	  echo "==> $$os/$$arch"; \
	  CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -ldflags "$(LDFLAGS) $(LDFLAGS_AGENT)" -o bin/larry-agent-$$os-$$arch ./cmd/larry-agent; \
	  CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -ldflags "$(LDFLAGS)" -o bin/larry-server-$$os-$$arch ./cmd/larry-server; \
	done

test:
	go test ./...

vet:
	go vet ./...

fmt:
	@go fmt ./...
	@gofmt -l . | grep -q . && { echo "files need formatting:"; gofmt -l .; exit 1; } || echo "format ok"

run: server
	./bin/larry-server --addr :80

clean:
	rm -rf bin/
