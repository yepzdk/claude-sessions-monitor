.PHONY: build build-all install packages checksums clean fmt lint shellcheck biome deadcode check

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
# Build tags hide code from a single pass: internal/jump is a darwin file and a
# linux file with no shared fallback, so whichever GOOS runs, the other's files
# go untyped. Naming both, rather than letting one of them be the host, is what
# makes a macOS machine lint the same pair a Linux one does. No file here is
# constrained by architecture, so GOARCH is pinned for that reason alone: to
# keep the host's out of the result.
lint:
	@$(GOLANGCI_LINT) --version 2>/dev/null | grep -q ' $(patsubst v%,%,$(GOLANGCI_LINT_VERSION)) ' || \
		go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	GOOS=linux GOARCH=amd64 $(GOLANGCI_LINT) run ./...
	GOOS=darwin GOARCH=arm64 $(GOLANGCI_LINT) run ./...

# Pinned so a new x/tools release cannot change what the gate reports. CI runs
# this target rather than installing its own copy, so this is the only pin.
DEADCODE_VERSION := v0.49.0
DEADCODE := $(or $(shell go env GOBIN),$(shell go env GOPATH)/bin)/deadcode

# golangci-lint's unused reads only the files one pass compiles, and never
# reports an exported name, because any importer could call it. An exported
# function with no caller anywhere is therefore invisible to it. deadcode walks
# the call graph from main instead, so it sees one.
#
# Each run sees one build, so a function the other platform's file calls looks
# unreachable here: internal/jump's pick sits in an untagged file and only
# jump_darwin.go calls it, so the linux pass reports it. -test counts a function
# a test reaches as live, and pick_test.go is what clears it. It cuts the other
# way too: a helper whose last production caller is deleted stays live as long
# as its test calls it.
#
# That is coverage doing the work, not a rule. A cross-platform helper with no
# test is still reported, wrongly. Add the test rather than delete the function.
# Judging it properly means reporting a function only when every build that
# compiles its file calls it dead, which needs the file sets from go list and a
# script to match them against deadcode -json.
#
# deadcode exits 0 whether or not it found anything, so the output is the
# result and the gate has to be built from it. A run that fails prints to
# stderr and nothing to stdout, which would read as "no dead code", so a
# non-zero exit is turned into output rather than left to the exit code.
deadcode:
	@go version -m $(DEADCODE) 2>/dev/null | grep -q 'golang.org/x/tools.*$(DEADCODE_VERSION)' || \
		go install golang.org/x/tools/cmd/deadcode@$(DEADCODE_VERSION)
	@found=$$({ GOOS=linux GOARCH=amd64 $(DEADCODE) -test ./... || echo "deadcode failed for GOOS=linux (see above)"; \
	            GOOS=darwin GOARCH=arm64 $(DEADCODE) -test ./... || echo "deadcode failed for GOOS=darwin (see above)"; } | sort -u); \
		[ -z "$$found" ] || { echo "$$found"; echo "Delete the unreachable function, call it, or fix the run that failed."; exit 1; }

# The web dashboard's JS and CSS, which no Go test reads and gofmt never sees.
# Pinned to match .github/workflows/ci.yaml, for the same reason the
# golangci-lint pin exists: a gate that reports different findings locally and
# in CI is worse than none.
#
# `check` is lint plus a formatting check. Biome ships a standalone binary, so
# nothing here needs Node or a package.json -- the frontend keeps its
# no-build-step property. `biome check --write .` fixes what it can.
#
# Skipped with a note rather than failing when it is absent, the same as
# shellcheck: it is not part of the Go toolchain, so requiring it would make
# `make check` unrunnable on a machine that can build and test fine. Probed by
# running it, not with `command -v`, because a version manager can leave a shim
# on PATH that exists but fails.
BIOME_VERSION := 2.5.13

# A wrong version cannot be corrected here the way lint and deadcode correct
# theirs: Biome is not a Go module, so there is no `go install` to fall back on.
# It runs anyway and says which version it used. Any install drifts from a
# hand-edited pin sooner or later, and `check` only reads, so the worst a
# mismatch does is give a verdict CI disagrees with. Refusing to run trades
# that for no local gate. CI installs the pin and is the one that decides.
biome:
	@biome --version >/dev/null 2>&1 || { echo "biome not installed -- skipping (CI will run it)"; exit 0; }; \
	installed=$$(biome --version | awk '{print $$NF}'); \
	[ "$$installed" = "$(BIOME_VERSION)" ] || \
		echo "note: biome $$installed installed, $(BIOME_VERSION) pinned -- CI decides"; \
	biome check . && echo "biome: 0 issues"

# Everything CI enforces, runnable locally before pushing
check:
	@gofmt -l . | grep . && { echo "Not gofmt-clean — run 'make fmt'"; exit 1; } || true
	go vet ./...
	$(MAKE) lint
	$(MAKE) shellcheck
	$(MAKE) biome
	$(MAKE) deadcode
	go build $(LDFLAGS) -o /dev/null .
	go test ./...

# The two POSIX sh scripts CI checks. Skipped with a note rather than failing
# when shellcheck is absent: it is not part of the Go toolchain, so requiring it
# would make `make check` unrunnable on a machine that can build and test fine.
# CI's runner always has it, so nothing merges unchecked. Probed by running it,
# not with `command -v`: a version manager (mise) can leave a shim on PATH that
# exists but fails, and that must count as absent.
shellcheck:
	@shellcheck --version >/dev/null 2>&1 || { echo "shellcheck not installed — skipping (CI will run it)"; exit 0; }; \
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
