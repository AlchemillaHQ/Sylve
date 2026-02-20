BINARY_NAME := sylve
BIN_DIR := bin

.PHONY: all build clean run depcheck

all: build

build: build-depcheck
	npm install --prefix web
	npm run build --prefix web
	cp -rf web/build/* internal/assets/web-files
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(BINARY_NAME) cmd/sylve/main.go

clean:
	rm -rf $(BIN_DIR)

run: build
	./$(BIN_DIR)/$(BINARY_NAME)

test:
	go test ./...

build-depcheck:
	@./scripts/build-deps-check.sh
