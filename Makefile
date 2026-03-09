BINARY_NAME := sylve
BIN_DIR := bin
ARCH ?= amd64

.PHONY: all build backend frontend test clean

all: build

build: frontend backend

backend:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=1 GOOS=freebsd GOARCH=$(ARCH) \
	go build -o $(BIN_DIR)/$(BINARY_NAME) cmd/sylve/main.go

frontend:
	npm ci --prefix web
	npm run build --prefix web
	mkdir -p internal/assets/web-files
	cp -rf web/build/* internal/assets/web-files/

test:
	go test ./...

clean:
	rm -rf $(BIN_DIR)
	rm -rf internal/assets/web-files/*
