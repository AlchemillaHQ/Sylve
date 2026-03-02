BINARY_NAME := sylve
BIN_DIR := bin

PLATFORMS := amd64 arm64 riscv64

.PHONY: all build build-all clean run test build-depcheck

all: build

build: build-depcheck web
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=1 \
	GOOS=freebsd GOARCH=amd64 go build -o $(BIN_DIR)/$(BINARY_NAME)-amd64 cmd/sylve/main.go

build-all: build-depcheck web
	mkdir -p $(BIN_DIR)
	@for arch in $(PLATFORMS); do \
		CGO_ENABLED=1 \
		GOOS=freebsd GOARCH=$$arch \
		go build -o $(BIN_DIR)/$(BINARY_NAME)-$$arch cmd/sylve/main.go ; \
	done

web:
	npm install --prefix web
	npm run build --prefix web
	cp -rf web/build/* internal/assets/web-files

clean:
	rm -rf $(BIN_DIR)

run: build
	./$(BIN_DIR)/$(BINARY_NAME)-amd64

test:
	go test ./...

build-depcheck:
	@./scripts/build-deps-check.sh