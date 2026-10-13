.PHONY: help all check test coverage coverage-html coverage-check coverage-clean fmt fmt-check vet lint lint-ci vuln \
	frontend frontend-install frontend-check build build-gui build-daemon build-daemon-all bundle-macos docker docker-all docker-run dev clean

help: ## This help
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z0-9_-]+:.*?## / {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.DEFAULT_GOAL := help

# Ignore a parent go.work (e.g. otfabric/go.work) so this module builds standalone.
export GOWORK := off

# CI formats and lints with the Go version from go.mod, not with the toolchain installed here.
# gofmt's output changes between Go releases, so formatting is done with that version, and
# `fmt-check` also requires the local gofmt to agree.
GO_MOD_VERSION := $(shell sed -nE 's/^go ([0-9]+\.[0-9]+).*/\1/p' go.mod | head -n1)
GOFMT_CI = $(shell GOTOOLCHAIN=go$(GO_MOD_VERSION).0 go env GOROOT)/bin/gofmt

# The webview bindings are built for the SDK's macOS version; link for the same minimum,
# or the linker warns about every object file.
ifeq ($(shell uname -s),Darwin)
export MACOSX_DEPLOYMENT_TARGET ?= 13.0
endif

GUI_NAME    = sunspec-explorer
GUI_SRC     = ./cmd/sunspec-explorer
DAEMON_NAME = sunspec-explorerd
DAEMON_SRC  = ./cmd/sunspec-explorerd
# The gateways: one static binary per architecture runs on Debian 11, 12 and 13.
DAEMON_ARCHS = linux/amd64 linux/arm64 linux/arm/v7
RELEASE_DIR = release
IMAGE      ?= sunspec-explorerd
IMAGE_PLATFORMS = linux/amd64,linux/arm64,linux/arm/v7
# Override on the command line or in CI. Release builds pass the bare number (VERSION=0.0.1).
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo dev)
LDFLAGS     = -ldflags "-s -w -X github.com/otfabric/sunspec-explorer/internal/buildinfo.Version=$(VERSION)"

# Packages measured by the coverage gate: the engine, the daemon and what they are built
# from. The Wails shell (cmd/sunspec-explorer, internal/gui) needs a window and is covered
# by the frontend tests.
TEST_PKGS     := ./internal/engine/... ./internal/readonly/... ./internal/service/... ./internal/model/... ./internal/buildinfo/... \
	./internal/api/... ./internal/web/... ./internal/daemon/... ./cmd/sunspec-explorerd/...
COVERAGE_MIN  := 90

all: check build ## Run all checks and build

check: fmt fmt-check vet lint lint-ci vuln test coverage-check frontend-check ## Run all checks (Go and frontend)

test: ## Run the Go tests with the race detector
	@echo "Running tests (race detector)"
	@go test -count=1 -race ./...

coverage: ## Run tests with coverage profile and text summary
	@echo "Running coverage on $(TEST_PKGS)"
	@go test -count=1 -race -coverprofile=coverage.out -covermode=atomic $(TEST_PKGS)
	@go tool cover -func=coverage.out | tee coverage.txt

coverage-html: coverage ## Generate HTML coverage report (coverage.html)
	@go tool cover -html=coverage.out -o coverage.html

coverage-check: ## Fail if total coverage is below $(COVERAGE_MIN)%
	@echo "Running coverage check (minimum $(COVERAGE_MIN)%) on $(TEST_PKGS)"
	@go test -count=1 -race -coverprofile=coverage.out -covermode=atomic $(TEST_PKGS)
	@go tool cover -func=coverage.out | tee coverage.txt
	@go tool cover -func=coverage.out | grep 'total:' | awk -v min=$(COVERAGE_MIN) '{gsub(/%/,""); p=$$NF+0; if (p < min) { printf "Coverage %.1f%% is below %d%%\n", p, min; exit 1 } else { printf "Coverage %.1f%% (>= %d%%)\n", p, min } }'

coverage-clean: ## Remove coverage artifacts
	@rm -f coverage.out coverage.txt coverage.html

fmt: ## Format Go code with the gofmt of the Go version in go.mod (what CI uses)
	@echo "Running gofmt (go$(GO_MOD_VERSION))"
	@$(GOFMT_CI) -w cmd internal frontend/*.go

fmt-check: ## Fail if the go.mod-version gofmt or the local gofmt would change any file
	@echo "Checking formatting (gofmt go$(GO_MOD_VERSION) and local $$(go env GOVERSION))"
	@ci="$$($(GOFMT_CI) -l cmd internal frontend/*.go)"; loc="$$(gofmt -l cmd internal frontend/*.go)"; \
	if [ -n "$$ci$$loc" ]; then \
		[ -z "$$ci" ] || { echo "Not formatted for gofmt go$(GO_MOD_VERSION) (CI):"; echo "$$ci"; }; \
		[ -z "$$loc" ] || { echo "Not formatted for the local gofmt:"; echo "$$loc"; }; \
		echo "Files must be stable under both. Run 'make fmt'."; \
		exit 1; \
	fi

vet: ## Run go vet on all packages
	@echo "Running go vet"
	@go vet ./...

lint: ## Run staticcheck
	@echo "Running staticcheck"
	@staticcheck ./...

lint-ci: ## Run golangci-lint (uses .golangci.yml; includes the read-only ban)
	@echo "Running golangci-lint"
	@golangci-lint run ./...

vuln: ## Run govulncheck
	@echo "Running govulncheck"
	@govulncheck ./...

frontend-install: ## Install the frontend dependencies (npm ci)
	@cd frontend && npm ci

frontend: ## Build the frontend twice: dist/wails (desktop) and dist/web (served by the daemon)
	@echo "Building frontend (wails + web)"
	@cd frontend && npm run build

frontend-check: ## Typecheck and test the frontend
	@echo "Checking frontend"
	@cd frontend && npm run typecheck && npm test

build: build-gui build-daemon ## Build everything into ./bin

build-daemon: frontend ## Build the daemon for this machine (static, no cgo, web UI embedded)
	@mkdir -p bin
	@echo "Building $(DAEMON_NAME) $(VERSION)"
	@CGO_ENABLED=0 go build -trimpath $(LDFLAGS) -o bin/$(DAEMON_NAME) $(DAEMON_SRC)

build-daemon-all: frontend ## Cross-build the daemon for the gateways (linux amd64, arm64, armv7)
	@mkdir -p $(RELEASE_DIR)
	@for arch in $(DAEMON_ARCHS); do \
		os=$${arch%%/*}; rest=$${arch#*/}; cpu=$${rest%%/*}; \
		if [ "$$cpu" = "arm" ]; then name=$(DAEMON_NAME)-$$os-armv7; arm=7; else name=$(DAEMON_NAME)-$$os-$$cpu; arm=; fi; \
		echo "Building $$name..."; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$cpu GOARM=$$arm go build -trimpath $(LDFLAGS) -o $(RELEASE_DIR)/$$name $(DAEMON_SRC) || exit 1; \
	done

build-gui: frontend ## Build the desktop application (needs cgo and the platform's webview)
	@mkdir -p bin
	@echo "Building $(GUI_NAME) $(VERSION)"
	@go build -tags production $(LDFLAGS) -o bin/$(GUI_NAME) $(GUI_SRC)

bundle-macos: build-gui ## Wrap the desktop binary in bin/SunSpec Explorer.app (macOS, ad-hoc signed)
	@packaging/macos/bundle.sh bin/$(GUI_NAME) $(VERSION) bin

docker: ## Build the daemon's container image for this machine ($(IMAGE):$(VERSION) and :dev)
	@echo "Building image $(IMAGE):$(VERSION)"
	@docker build -f packaging/Dockerfile --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) -t $(IMAGE):dev .

docker-all: ## Check that the image builds for every gateway architecture (not loaded, not pushed)
	@docker buildx build -f packaging/Dockerfile --build-arg VERSION=$(VERSION) --platform $(IMAGE_PLATFORMS) .

docker-run: docker ## Run the daemon in a container on 127.0.0.1:8765; "docker exec sunspec-explorerd sunspec-explorerd url" prints the URL
	@docker run --rm --name sunspec-explorerd -p 127.0.0.1:8765:8765 -v sunspec-explorer-data:/data $(IMAGE):dev

dev: frontend ## Run the desktop application from source
	@go run $(GUI_SRC)

clean: coverage-clean ## Clean build artifacts
	@rm -rf bin release
