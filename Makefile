.PHONY: build build-all install packages checksums clean fmt lint check

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

# nfpm requires a version starting with a digit (deb policy), so strip any leading 'v'.
PKG_VERSION := $(patsubst v%,%,$(VERSION))

# Format all Go code (gofmt is the only style authority — see CONTRIBUTING.md)
fmt:
	gofmt -w .

# Pinned to match .github/workflows/ci.yaml — a lint gate that reports
# different findings locally and in CI is worse than none.
GOLANGCI_LINT_VERSION := v2.13.1
# go install writes to GOBIN when it is set, and to GOPATH/bin otherwise.
GOLANGCI_LINT := $(or $(shell go env GOBIN),$(shell go env GOPATH)/bin)/golangci-lint

# golangci-lint v2.13.1 is built with Go 1.26, so `go install` fetches that
# toolchain on a 1.25 machine. GOTOOLCHAIN=local blocks that and the install
# fails; leave GOTOOLCHAIN at its default.
#
# Build tags hide code from a single pass: internal/jump's real implementation
# is darwin-only and its stub is !darwin, so whichever GOOS runs, the other's
# file goes untyped. Naming both, rather than letting one of them be the host,
# is what makes a macOS machine lint the same pair a Linux one does. No file
# here is constrained by architecture, so GOARCH is pinned for that reason
# alone: to keep the host's out of the result.
lint:
	@$(GOLANGCI_LINT) --version 2>/dev/null | grep -q ' $(patsubst v%,%,$(GOLANGCI_LINT_VERSION)) ' || \
		go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	GOOS=linux GOARCH=amd64 $(GOLANGCI_LINT) run ./...
	GOOS=darwin GOARCH=arm64 $(GOLANGCI_LINT) run ./...

# Everything CI enforces, runnable locally before pushing
check:
	@gofmt -l . | grep . && { echo "Not gofmt-clean — run 'make fmt'"; exit 1; } || true
	go vet ./...
	$(MAKE) lint
	go build $(LDFLAGS) -o /dev/null .
	go test ./...

# Build for current platform
build:
	go build $(LDFLAGS) -o csm .

# Build for all platforms
build-all: clean
	@mkdir -p dist
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o dist/csm-darwin-amd64 .
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o dist/csm-darwin-arm64 .
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o dist/csm-linux-amd64 .
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o dist/csm-linux-arm64 .

# Install to ~/.local/bin
install: build
	@mkdir -p $(HOME)/.local/bin
	cp csm $(HOME)/.local/bin/csm
	@echo "Installed to $(HOME)/.local/bin/csm"

# Build .deb and .rpm Linux packages for amd64 and arm64.
# Requires `nfpm` on PATH (see .github/workflows for the pinned version used in CI).
packages: build-all
	@command -v nfpm >/dev/null 2>&1 || { echo >&2 "nfpm not found. Install from https://nfpm.goreleaser.com/install/"; exit 1; }
	@for arch in amd64 arm64; do \
		for pkg in deb rpm; do \
			echo "Building csm $(PKG_VERSION) $$arch $$pkg"; \
			VERSION=$(PKG_VERSION) ARCH=$$arch nfpm package --config nfpm.yaml --packager $$pkg --target dist/ || exit 1; \
		done; \
	done

# Hash every release asset into dist/checksums.txt.
#
# This file is the single source of truth for the hashes: install.sh, `csm
# -upgrade`, the Homebrew formula and the AUR PKGBUILD all read it rather than
# each hashing the binaries themselves, which is how those four drift apart.
#
# Deliberately has no prerequisites — `packages` rebuilds from `clean`, so
# depending on it here would wipe and rebuild dist/ a third time in CI. Run it
# after `make packages`.
checksums:
	@test -d dist || { echo >&2 "dist/ is empty — run 'make packages' first"; exit 1; }
	cd dist && sha256sum csm-darwin-* csm-linux-* csm_*.deb csm-*.rpm > checksums.txt
	@echo "Wrote dist/checksums.txt"

# Clean build artifacts
clean:
	rm -f csm
	rm -rf dist
