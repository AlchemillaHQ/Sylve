BINARY_NAME := sylve
BIN_DIR := bin
ARCH ?= amd64
FREEBSD_VERSION ?= 15.0-RELEASE
FREEBSD_SYSROOT ?= .cache/freebsd/$(ARCH)-$(FREEBSD_VERSION)
SMART_DEVICE ?=
SMART_RUN_SELF_TEST ?= 0
SMART_WAIT_SELF_TEST ?= 0
SMART_TEST_OUTPUT ?= tmp/smart-integration.log
SMART_TEST_TIMEOUT ?= 30m
GIT_COMMIT != git rev-parse --short HEAD 2>/dev/null || echo unknown

.PHONY: all build backend backend-debug backend-cross cross-build-amd64 cross-build-arm64 frontend test test-integration test-smart-integration clean

all: build

build: frontend backend

backend:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=1 GOOS=freebsd GOARCH=$(ARCH) \
		go build -ldflags="-s -w -X github.com/alchemillahq/sylve/internal/cmd.Commit=$(GIT_COMMIT)" -o $(BIN_DIR)/$(BINARY_NAME) ./cmd/sylve

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
	go build -ldflags="-s -w -X github.com/alchemillahq/sylve/internal/cmd.Commit=$(GIT_COMMIT)" -o $(BIN_DIR)/$(BINARY_NAME) ./cmd/sylve

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
	go test -short ./...

test-integration:
	@[ "$$(id -u)" = "0" ] || { echo "make test-integration must run as root (it creates ZFS pools)"; exit 1; }
	go test -count=1 -v ./...

test-smart-integration:
	@[ "$$(sysctl -n kern.ostype 2>/dev/null)" = "FreeBSD" ] || { echo "make test-smart-integration must run on FreeBSD"; exit 1; }
	@[ -n "$(SMART_DEVICE)" ] || { echo "usage: make test-smart-integration SMART_DEVICE=/dev/ada0"; echo "optional: SMART_RUN_SELF_TEST=1 or SMART_WAIT_SELF_TEST=1; SMART_TEST_OUTPUT=path; SMART_TEST_TIMEOUT=30m"; exit 1; }
	@[ -e "$(SMART_DEVICE)" ] || { echo "SMART_DEVICE does not exist: $(SMART_DEVICE)"; exit 1; }
	@[ "$(SMART_RUN_SELF_TEST)" = "0" ] || [ "$(SMART_RUN_SELF_TEST)" = "1" ] || { echo "SMART_RUN_SELF_TEST must be 0 or 1"; exit 1; }
	@[ "$(SMART_WAIT_SELF_TEST)" = "0" ] || [ "$(SMART_WAIT_SELF_TEST)" = "1" ] || { echo "SMART_WAIT_SELF_TEST must be 0 or 1"; exit 1; }
	@[ "$(SMART_RUN_SELF_TEST)" != "1" ] || [ "$(SMART_WAIT_SELF_TEST)" != "1" ] || { echo "SMART_RUN_SELF_TEST and SMART_WAIT_SELF_TEST cannot both be 1"; exit 1; }
	@mkdir -p "$$(dirname "$(SMART_TEST_OUTPUT)")"
	@set -o pipefail; { \
		printf 'Sylve SMART integration report\n'; \
		printf 'commit: %s\n' "$(GIT_COMMIT)"; \
		printf 'device: %s\n' "$(SMART_DEVICE)"; \
		printf 'start-abort-self-test: %s\n' "$(SMART_RUN_SELF_TEST)"; \
		printf 'wait-self-test: %s\n' "$(SMART_WAIT_SELF_TEST)"; \
		printf 'output: %s\n' "$(SMART_TEST_OUTPUT)"; \
		sysctl kern.ostype kern.osrelease kern.version hw.machine_arch; \
		go version; \
		SYLVE_SMART_TEST_DEVICE="$(SMART_DEVICE)" \
		SYLVE_SMART_RUN_SELF_TEST="$(SMART_RUN_SELF_TEST)" \
		SYLVE_SMART_WAIT_SELF_TEST="$(SMART_WAIT_SELF_TEST)" \
		go test -count=1 -timeout="$(SMART_TEST_TIMEOUT)" -v -run '^TestHardware' ./pkg/disk/smart; \
	} 2>&1 | tee "$(SMART_TEST_OUTPUT)"

clean:
	rm -rf $(BIN_DIR)
	rm -rf internal/assets/web-files/*
