BINARY_NAME := sylve
BIN_DIR := bin
ARCH ?= amd64
FREEBSD_VERSION ?= 15.0-RELEASE
FREEBSD_SYSROOT ?= .cache/freebsd/$(ARCH)-$(FREEBSD_VERSION)

.PHONY: all build backend backend-debug backend-cross cross-build-amd64 cross-build-arm64 frontend test clean

all: build

build: frontend backend

backend:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=1 GOOS=freebsd GOARCH=$(ARCH) \
		go build -ldflags="-s -w" -o $(BIN_DIR)/$(BINARY_NAME) ./cmd/sylve

backend-debug:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=1 GOOS=freebsd GOARCH=$(ARCH) \
		go build -gcflags="all=-N -l" -o $(BIN_DIR)/$(BINARY_NAME) ./cmd/sylve

backend-cross:
	mkdir -p $(BIN_DIR)
	@set -eu; \
	SYSROOT="$(FREEBSD_SYSROOT)"; \
	case "$$SYSROOT" in \
		/*) ;; \
		*) SYSROOT="$$(pwd)/$$SYSROOT" ;; \
	esac; \
	ARCH=$(ARCH) FREEBSD_VERSION=$(FREEBSD_VERSION) FREEBSD_SYSROOT="$$SYSROOT" \
		./scripts/setup-freebsd-sysroot.sh; \
	case "$(ARCH)" in \
		amd64) GOARCH=amd64; TARGET=x86_64-unknown-freebsd15.0 ;; \
		arm64) GOARCH=arm64; TARGET=aarch64-unknown-freebsd15.0 ;; \
		*) echo "Unsupported ARCH: $(ARCH)" >&2; exit 1 ;; \
	esac; \
	CGO_ENABLED=1 GOOS=freebsd GOARCH=$$GOARCH \
	CGO_CFLAGS="--sysroot=$$SYSROOT" \
	CGO_CPPFLAGS="--sysroot=$$SYSROOT" \
	CGO_CXXFLAGS="--sysroot=$$SYSROOT" \
	CGO_LDFLAGS="-fuse-ld=lld --sysroot=$$SYSROOT" \
	CC="clang --target=$$TARGET --sysroot=$$SYSROOT" \
	CXX="clang++ --target=$$TARGET --sysroot=$$SYSROOT" \
	go build -ldflags="-s -w" -o $(BIN_DIR)/$(BINARY_NAME) ./cmd/sylve

cross-build-amd64:
	$(MAKE) backend-cross ARCH=amd64

cross-build-arm64:
	$(MAKE) backend-cross ARCH=arm64

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
