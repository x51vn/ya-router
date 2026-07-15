BINARY ?= ya-router
DAEMON_BINARY ?= ya-routerd
CLIENT_BINARY ?= ya
VERSION ?= dev
VERSION_LDFLAG = -X github.com/duvu/ya-router/src.version=$(VERSION)

all: build

build:
	go build -trimpath -ldflags="-s -w $(VERSION_LDFLAG)" -o $(BINARY) ./cmd/ya-router

build-daemon:
	go build -trimpath -ldflags="-s -w $(VERSION_LDFLAG)" -o $(DAEMON_BINARY) ./cmd/ya-routerd

build-client:
	go build -trimpath -ldflags="-s -w $(VERSION_LDFLAG)" -o $(CLIENT_BINARY) ./cmd/ya

build-all: build build-daemon build-client

run: build
	./$(BINARY) run

auth: build
	./$(BINARY) auth

models: build
	./$(BINARY) models

config: build
	./$(BINARY) config

clean:
	rm -f $(BINARY) $(DAEMON_BINARY) $(CLIENT_BINARY)

fmt:
	gofmt -w ./cmd ./internal ./src

fmt-check:
	@test -z "$$(gofmt -l ./cmd ./internal ./src)" || (gofmt -l ./cmd ./internal ./src && exit 1)

vet:
	go vet ./...

test:
	go test -race -count=1 ./...

check: fmt-check vet test build-all

help:
	@echo "Targets: build build-daemon build-client build-all run auth models config clean fmt fmt-check vet test check"

.PHONY: all build build-daemon build-client build-all run auth models config clean fmt fmt-check vet test check help
