SHELL := /bin/sh

BIN_DIR := bin
VERSION ?= 1.0.0-beta
GO ?= go
PNPM ?= pnpm
LDFLAGS := -s -w -buildvcs=false -X main.version=$(VERSION)

.PHONY: all build build-server build-agent build-linux \
	web-install web-dev web-build fmt vet test clean

all: build web-build

build: build-server build-agent

build-server:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/nodehub-server ./server

build-agent:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/nodehub-agent ./agent

build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/nodehub-server-linux-amd64 ./server
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/nodehub-agent-linux-amd64 ./agent

web-install:
	$(PNPM) --dir web install

web-dev:
	$(PNPM) --dir web dev

web-build:
	$(PNPM) --dir web build

fmt:
	gofmt -w server agent internal scripts

vet:
	CGO_ENABLED=0 $(GO) vet ./...

test:
	CGO_ENABLED=0 $(GO) test ./...

clean:
	rm -rf $(BIN_DIR) web/dist
