BINARY_NAME := sylve
BIN_DIR := bin
ARCH ?= amd64

.PHONY: all build clean web-build

all: build

build: 
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=1 GOOS=freebsd GOARCH=$(ARCH) \
	go build -o $(BIN_DIR)/$(BINARY_NAME)-$(ARCH) cmd/sylve/main.go

web-build:
	npm install --prefix web
	npm run build --prefix web
	mkdir -p internal/assets/web-files
	cp -rf web/build/* internal/assets/web-files/

clean:
	rm -rf $(BIN_DIR)
	rm -rf internal/assets/web-files/*