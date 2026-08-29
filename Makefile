.PHONY: build build-all install packages checksums clean fmt lint shellcheck check

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
	$(MAKE) shellcheck
	go build $(LDFLAGS) -o /dev/null .
	go test ./...

# The two POSIX sh scripts CI checks. Skipped with a note rather than failing
# when shellcheck is absent: it is not part of the Go toolchain, so requiring it
# would make `make check` unrunnable on a machine that can build and test fine.
# CI's runner always has it, so nothing merges unchecked.
shellcheck:
	@command -v shellcheck >/dev/null 2>&1 || { echo "shellcheck not installed — skipping (CI will run it)"; exit 0; }; \
		shellcheck --shell=sh install.sh packaging/aur/render.sh && echo "shellcheck: 0 issues"

# Build for current platform
build:
	go build $(LDFLAGS) -o csm .

# Build for all platforms.
#
# CGO_ENABLED=0 on every target so all four binaries are statically linked.
# Without it the host-architecture build picks up cgo (net pulls it in when a C
# compiler is present) and links against the build machine's glibc, while the
# cross-compiled ones come out static -- so the amd64 release binary refused to
# run on any distro older than the CI runner while the arm64 one ran anywhere.
# Nothing here needs cgo: the pure-Go resolver is fine for the three HTTPS
# endpoints csm talks to, and no package uses os/user.
build-all: clean
	@mkdir -p dist
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o dist/csm-darwin-amd64 .
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o dist/csm-darwin-arm64 .
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o dist/csm-linux-amd64 .
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o dist/csm-linux-arm64 .

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
# Written through a temp file: an unmatched glob is passed through literally and
# sha256sum errors on it, but `>` has already truncated the target, so a direct
# redirect leaves a *partial* single source of truth on disk. Reachable with
# `make build-all && make checksums` before any package exists.
checksums:
	@test -d dist || { echo >&2 "dist/ is empty — run 'make packages' first"; exit 1; }
	cd dist && { sha256sum csm-darwin-* csm-linux-* csm_*.deb csm-*.rpm > checksums.txt.tmp || { rm -f checksums.txt.tmp; exit 1; }; } && mv checksums.txt.tmp checksums.txt
	@echo "Wrote dist/checksums.txt"

# Clean build artifacts
clean:
	rm -f csm
	rm -rf dist
