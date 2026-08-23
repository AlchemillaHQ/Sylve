BINARY_NAME := sylve
BIN_DIR := bin
ARCH ?= amd64
FREEBSD_VERSION ?= 15
FREEBSD_SYSROOT ?=
SMART_DEVICE ?=
SMART_RUN_SELF_TEST ?= 0
SMART_WAIT_SELF_TEST ?= 0
SMART_TEST_OUTPUT ?= tmp/smart-integration.log
SMART_TEST_TIMEOUT ?= 30m
INTEGRATION_TEST_TIMEOUT ?= 45m
GO_TEST_FLAGS ?=
GIT_COMMIT != git rev-parse --short HEAD 2>/dev/null || echo unknown

INTEGRATION_PACKAGES := \
	./internal/mountutil \
	./internal/services/disk \
	./internal/services/jail \
	./internal/services/libvirt \
	./internal/services/migration \
	./internal/services/network \
	./internal/services/cluster \
	./internal/services/zelta \
	./internal/services/zfs \
	./internal/zfsutil \
	./pkg/network/mdns

ACCEPTANCE_PACKAGE := ./internal/console/integration

.PHONY: all build backend backend-debug backend-cross cross-build-amd64 cross-build-arm64 frontend quality quality-fix
.PHONY: test test-external-preflight test-integration test-acceptance test-acceptance-full test-acceptance-all
.PHONY: test-smart-integration clean

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
	RESOLVED_FREEBSD_VERSION="$$(ARCH="$(ARCH)" FREEBSD_VERSION="$(FREEBSD_VERSION)" ./scripts/resolve-freebsd-release.sh)"; \
	SYSROOT="$(FREEBSD_SYSROOT)"; \
	if [ -z "$$SYSROOT" ]; then \
		SYSROOT=".cache/freebsd/$(ARCH)-$$RESOLVED_FREEBSD_VERSION"; \
	fi; \
	case "$$SYSROOT" in \
		/*) ;; \
		*) SYSROOT="$$(pwd)/$$SYSROOT" ;; \
	esac; \
	ARCH="$(ARCH)" FREEBSD_VERSION="$$RESOLVED_FREEBSD_VERSION" FREEBSD_SYSROOT="$$SYSROOT" \
		./scripts/setup-freebsd-sysroot.sh; \
	TARGET_VERSION="$${RESOLVED_FREEBSD_VERSION%-RELEASE}"; \
	case "$(ARCH)" in \
		amd64) GOARCH=amd64; TARGET="x86_64-unknown-freebsd$$TARGET_VERSION" ;; \
		arm64) GOARCH=arm64; TARGET="aarch64-unknown-freebsd$$TARGET_VERSION" ;; \
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
	npm run check --prefix web
	npm run build --prefix web
	mkdir -p internal/assets/web-files
	cp -rf web/build/* internal/assets/web-files/

quality:
	@unformatted="$$(git ls-files -z --cached --others --exclude-standard -- '*.go' | \
		xargs -0 sh -c 'for file do [ -f "$$file" ] && printf "./%s\000" "$$file"; done' sh | \
		xargs -0 gofmt -l)"; \
	if [ -n "$$unformatted" ]; then \
		printf 'Unformatted Go files:\n%s\n' "$$unformatted"; \
		exit 1; \
	fi
	npm ci --prefix web
	npm run lint --prefix web
	npm run check --prefix web

quality-fix:
	git ls-files -z --cached --others --exclude-standard -- '*.go' | \
		xargs -0 sh -c 'for file do [ -f "$$file" ] && printf "./%s\000" "$$file"; done' sh | \
		xargs -0 gofmt -w
	npm run lint:fix --prefix web

test:
	go test $(GO_TEST_FLAGS) -short ./... ./internal/testutil/zfstest

test-external-preflight:
	@[ "$$(uname -s)" = "FreeBSD" ] || { echo "external-state tests must run on FreeBSD"; exit 1; }
	@[ "$$(id -u)" = "0" ] || { echo "external-state tests must run as root"; exit 1; }
	@command -v zpool >/dev/null || { echo "zpool is required for external-state tests"; exit 1; }
	@command -v zfs >/dev/null || { echo "zfs is required for external-state tests"; exit 1; }

test-integration: test-external-preflight
	@./scripts/check-zfs-test-leaks.sh
	@set +e; \
	go test $(GO_TEST_FLAGS) -count=1 -p=2 \
		-timeout="$(INTEGRATION_TEST_TIMEOUT)" -v \
		-run '^TestIntegration' $(INTEGRATION_PACKAGES); \
	test_rc="$$?"; \
	./scripts/check-zfs-test-leaks.sh; \
	leak_rc="$$?"; \
	if [ "$$test_rc" -ne 0 ]; then exit "$$test_rc"; fi; \
	exit "$$leak_rc"

test-acceptance: test-external-preflight
	go test $(GO_TEST_FLAGS) -count=1 -p=1 -timeout="$(INTEGRATION_TEST_TIMEOUT)" -v \
		-run '^TestAcceptance' $(ACCEPTANCE_PACKAGE)

test-acceptance-full: test-external-preflight
	go test $(GO_TEST_FLAGS) -count=1 -p=1 -timeout="$(INTEGRATION_TEST_TIMEOUT)" -v \
		-run '^TestFullAcceptance' $(ACCEPTANCE_PACKAGE)

test-acceptance-all: test-external-preflight
	@./scripts/check-console-test-leaks.sh
	@set +e; \
	go test $(GO_TEST_FLAGS) -count=1 -p=1 -timeout="$(INTEGRATION_TEST_TIMEOUT)" -v \
		-run '^(TestAcceptance|TestFullAcceptance)' $(ACCEPTANCE_PACKAGE); \
	test_rc="$$?"; \
	./scripts/check-console-test-leaks.sh; \
	leak_rc="$$?"; \
	if [ "$$test_rc" -ne 0 ]; then exit "$$test_rc"; fi; \
	exit "$$leak_rc"

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
