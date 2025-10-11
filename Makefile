BINARY_NAME := sylve
BIN_DIR := bin

.PHONY: all build clean run depcheck vendor-setup

all: build

build: build-depcheck vendor-setup
	npm install --prefix web
	npm run build --prefix web
	cp -rf web/build/* internal/assets/web-files
	mkdir -p $(BIN_DIR)
	CGO_CFLAGS="-I/usr/local/include" CGO_LDFLAGS="-L/usr/local/lib" go build -o $(BIN_DIR)/$(BINARY_NAME) cmd/sylve/main.go

vendor-setup:
	go mod vendor
	ln -sf /usr/local/include/aria2 vendor/github.com/coolerfall/aria2go/aria2

clean:
	rm -rf $(BIN_DIR)
	rm -rf vendor

run: build
	./$(BIN_DIR)/$(BINARY_NAME)

test:
	go test ./...

build-depcheck:
	@./scripts/build-deps-check.sh
